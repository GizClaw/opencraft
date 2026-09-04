// Package undo implements conversation-scoped workspace rollback.
// Before each agent turn the desktop captures the content of every
// file git reports as changed or untracked; Undo restores the latest
// captured pre-turn state and Redo re-applies the post-turn state.
// Only git repositories are supported in v1; non-git workspaces
// simply have no snapshots.
package undo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxFileBytes caps one snapshot file. Larger files are recorded as
// skipped and left untouched by undo/redo (restoring them would
// require unbounded memory).
const MaxFileBytes = 4 << 20

// maxLiveEntries bounds retained snapshots per conversation.
const maxLiveEntries = 20

// maxUndoBytes is the global budget for all undo snapshots across every
// conversation (live + undone). Oldest entries are pruned once the
// budget is exceeded, so a workspace with many changed files cannot
// fill the disk without bound.
var maxUndoBytes = int64(256 << 20) // 256 MiB

// Snapshot file kinds.
const (
	KindFile    = "file"
	KindSymlink = "symlink"
)

// FileState is one file's content at a snapshot point. Present=false
// means the file did not exist (undo removes it, redo recreates it).
type FileState struct {
	Path       string `json:"path"`
	Present    bool   `json:"present"`
	Kind       string `json:"kind,omitempty"` // KindFile (default) | KindSymlink
	Mode       uint32 `json:"mode,omitempty"` // permission bits for regular files
	Content    []byte `json:"content,omitempty"`
	LinkTarget string `json:"link_target,omitempty"`
	Skipped    bool   `json:"skipped,omitempty"`
}

// UnmarshalJSON decodes both the current schema (kind + base64
// content) and legacy snapshots that stored content as a plain JSON
// string before kinds and modes existed.
func (f *FileState) UnmarshalJSON(data []byte) error {
	var raw struct {
		Path       string          `json:"path"`
		Present    bool            `json:"present"`
		Kind       string          `json:"kind,omitempty"`
		Mode       uint32          `json:"mode,omitempty"`
		Content    json.RawMessage `json:"content,omitempty"`
		LinkTarget string          `json:"link_target,omitempty"`
		Skipped    bool            `json:"skipped,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.Path = raw.Path
	f.Present = raw.Present
	f.Kind = raw.Kind
	f.Mode = raw.Mode
	f.LinkTarget = raw.LinkTarget
	f.Skipped = raw.Skipped
	if len(raw.Content) == 0 {
		return nil
	}
	if raw.Kind == "" {
		var legacy string
		if err := json.Unmarshal(raw.Content, &legacy); err != nil {
			return fmt.Errorf("undo: decode legacy content: %w", err)
		}
		f.Content = []byte(legacy)
		return nil
	}
	if err := json.Unmarshal(raw.Content, &f.Content); err != nil {
		return fmt.Errorf("undo: decode content: %w", err)
	}
	return nil
}

// Entry is one captured before/after pair for a conversation turn.
type Entry struct {
	Seq     int         `json:"seq"`
	Created time.Time   `json:"created_at"`
	Before  []FileState `json:"before"`
	After   []FileState `json:"after"`
}

// State is the UI-facing undo availability.
type State struct {
	CanUndo bool `json:"can_undo"`
	CanRedo bool `json:"can_redo"`
}

// ErrEmpty is returned by Undo/Redo when there is nothing to apply.
var ErrEmpty = errors.New("undo: nothing to apply")

// Store is a workspace snapshot store. Snapshots live under
// <undoRoot>/<contextID>/, split into live/ and undone/ so undo/redo
// move entries instead of rewriting them. undoRoot is injected by the
// Host; workDir is only used to resolve snapshot paths back into the
// project.
type Store struct {
	workDir string
	root    string
	mu      sync.Mutex
}

// NewWithRoot creates a store whose snapshots live under root. workDir
// is the actual project root used when applying/restoring files.
func NewWithRoot(workDir, root string) *Store {
	return &Store{
		workDir: workDir,
		root:    root,
	}
}

// Capture records one before/after pair for the conversation. An
// entry whose states are identical is dropped. New work after an undo
// clears the redo stack, matching editor semantics.
func (s *Store) Capture(
	_ context.Context,
	contextID string,
	before, after []FileState,
) (int, error) {
	if equalStates(before, after) {
		return 0, nil
	}
	liveDir, undoneDir, err := s.dirs(contextID)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Any new turn invalidates the redo stack.
	if err := os.RemoveAll(undoneDir); err != nil {
		return 0, fmt.Errorf("undo: clear redo stack: %w", err)
	}
	seqs, err := listSeqs(liveDir)
	if err != nil {
		return 0, err
	}
	seq := 0
	if len(seqs) > 0 {
		seq = seqs[len(seqs)-1]
	}
	seq++
	entry := Entry{
		Seq:     seq,
		Created: time.Now().UTC(),
		Before:  before,
		After:   after,
	}
	if err := writeEntry(filepath.Join(liveDir, entryName(seq)), entry); err != nil {
		return 0, err
	}
	pruneLive(liveDir)
	s.pruneGlobal()
	return seq, nil
}

// Undo restores the latest live snapshot's before-state and moves it
// to the redo stack. It returns the paths that were touched.
func (s *Store) Undo(_ context.Context, contextID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	liveDir, undoneDir, err := s.dirs(contextID)
	if err != nil {
		return nil, err
	}
	seq, err := latestSeq(liveDir)
	if err != nil {
		return nil, err
	}
	entry, err := readEntry(filepath.Join(liveDir, entryName(seq)))
	if err != nil {
		return nil, err
	}
	changed, err := s.apply(entry.Before)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(undoneDir, 0o700); err != nil {
		return nil, fmt.Errorf("undo: mkdir redo dir: %w", err)
	}
	_ = os.Chmod(undoneDir, 0o700)
	if err := os.Rename(
		filepath.Join(liveDir, entryName(seq)),
		filepath.Join(undoneDir, entryName(seq)),
	); err != nil {
		return nil, fmt.Errorf("undo: move entry: %w", err)
	}
	return changed, nil
}

// Redo re-applies the latest undone snapshot's after-state.
func (s *Store) Redo(_ context.Context, contextID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, undoneDir, err := s.dirs(contextID)
	if err != nil {
		return nil, err
	}
	seq, err := latestSeq(undoneDir)
	if err != nil {
		return nil, err
	}
	entry, err := readEntry(filepath.Join(undoneDir, entryName(seq)))
	if err != nil {
		return nil, err
	}
	changed, err := s.apply(entry.After)
	if err != nil {
		return nil, err
	}
	liveDir, _, err := s.dirs(contextID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(liveDir, 0o700); err != nil {
		return nil, fmt.Errorf("undo: mkdir live dir: %w", err)
	}
	_ = os.Chmod(liveDir, 0o700)
	if err := os.Rename(
		filepath.Join(undoneDir, entryName(seq)),
		filepath.Join(liveDir, entryName(seq)),
	); err != nil {
		return nil, fmt.Errorf("undo: move entry: %w", err)
	}
	return changed, nil
}

// Available reports whether undo/redo have anything to apply.
func (s *Store) Available(_ context.Context, contextID string) (State, error) {
	liveDir, undoneDir, err := s.dirs(contextID)
	if err != nil {
		return State{}, err
	}
	live, err := latestSeq(liveDir)
	if err != nil && !errors.Is(err, ErrEmpty) {
		return State{}, err
	}
	undone, err := latestSeq(undoneDir)
	if err != nil && !errors.Is(err, ErrEmpty) {
		return State{}, err
	}
	return State{CanUndo: live > 0, CanRedo: undone > 0}, nil
}

func (s *Store) dirs(contextID string) (live, undone string, err error) {
	id := strings.TrimSpace(contextID)
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return "", "", fmt.Errorf("undo: invalid context id %q", contextID)
	}
	base := filepath.Join(s.root, id)
	liveDir := filepath.Join(base, "live")
	undoneDir := filepath.Join(base, "undone")
	for _, dir := range []string{liveDir, undoneDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", fmt.Errorf("undo: mkdir %s: %w", dir, err)
		}
		_ = os.Chmod(dir, 0o700)
	}
	return liveDir, undoneDir, nil
}

// apply writes/deletes files per the given states. Skipped states are
// left untouched. All errors are collected; paths already handled are
// still reported so the UI shows partial progress.
func (s *Store) apply(states []FileState) ([]string, error) {
	var changed []string
	var errs []error
	for _, st := range states {
		if st.Skipped {
			continue
		}
		path, err := s.resolve(st.Path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !st.Present {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("undo: remove %s: %w", st.Path, err))
				continue
			}
			changed = append(changed, st.Path)
			continue
		}
		kind := st.Kind
		if kind == "" {
			kind = KindFile
		}
		switch kind {
		case KindSymlink:
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf(
					"undo: remove old %s: %w", st.Path, err))
				continue
			}
			if err := os.Symlink(st.LinkTarget, path); err != nil {
				errs = append(errs, fmt.Errorf(
					"undo: symlink %s -> %s: %w",
					st.Path, st.LinkTarget, err))
				continue
			}
		default:
			if err := writeStateFile(path, st.Content, st.Mode); err != nil {
				errs = append(errs, fmt.Errorf("undo: write %s: %w", st.Path, err))
				continue
			}
		}
		changed = append(changed, st.Path)
	}
	return changed, errors.Join(errs...)
}

// writeStateFile writes content through a same-directory temp file and
// renames it over the target. Rename replaces a symlink instead of
// following it, so restoring a snapshot can never redirect the write
// outside the workspace.
func writeStateFile(path string, data []byte, mode uint32) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", path, err)
	}
	perm := os.FileMode(mode).Perm()
	if perm == 0 {
		perm = 0o644
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".undo-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

func (s *Store) resolve(rel string) (string, error) {
	rel = filepath.FromSlash(rel)
	if rel == "" || filepath.IsAbs(rel) || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("undo: unsafe snapshot path %q", rel)
	}
	path := filepath.Join(s.workDir, rel)
	root, err := filepath.Abs(s.workDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relCheck, err := filepath.Rel(root, abs)
	if err != nil || relCheck == ".." ||
		strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("undo: path escapes workspace %q", rel)
	}
	return abs, nil
}

func entryName(seq int) string { return fmt.Sprintf("%06d.json", seq) }

func writeEntry(path string, entry Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("undo: mkdir: %w", err)
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("undo: encode entry: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("undo: write entry: %w", err)
	}
	return nil
}

func readEntry(path string) (Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("undo: read entry: %w", err)
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, fmt.Errorf("undo: decode entry: %w", err)
	}
	return entry, nil
}

func listSeqs(dir string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("undo: list snapshots: %w", err)
	}
	var seqs []int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var seq int
		if _, err := fmt.Sscanf(e.Name(), "%06d.json", &seq); err == nil {
			seqs = append(seqs, seq)
		}
	}
	sort.Ints(seqs)
	return seqs, nil
}

func latestSeq(dir string) (int, error) {
	seqs, err := listSeqs(dir)
	if err != nil {
		return 0, err
	}
	if len(seqs) == 0 {
		return 0, ErrEmpty
	}
	return seqs[len(seqs)-1], nil
}

// pruneLive keeps only the newest maxLiveEntries snapshots.
func pruneLive(dir string) {
	seqs, err := listSeqs(dir)
	if err != nil {
		return
	}
	if len(seqs) <= maxLiveEntries {
		return
	}
	for _, seq := range seqs[:len(seqs)-maxLiveEntries] {
		_ = os.Remove(filepath.Join(dir, entryName(seq)))
	}
}

// pruneGlobal removes the oldest undo entries across every
// conversation once the total snapshot size exceeds maxUndoBytes. The
// most recent entry is always kept even when it alone exceeds the
// budget.
func (s *Store) pruneGlobal() {
	type entry struct {
		path string
		mod  time.Time
		size int64
	}
	var entries []entry
	var total int64
	_ = filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".json") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, entry{
			path: path, mod: info.ModTime(), size: info.Size(),
		})
		total += info.Size()
		return nil
	})
	if total <= maxUndoBytes || len(entries) <= 1 {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].mod.Before(entries[j].mod)
	})
	for i := 0; i < len(entries)-1 && total > maxUndoBytes; i++ {
		if err := os.Remove(entries[i].path); err == nil {
			total -= entries[i].size
		}
	}
}

func equalStates(a, b []FileState) bool {
	if len(a) != len(b) {
		return false
	}
	bm := make(map[string]FileState, len(b))
	for _, st := range b {
		bm[st.Path] = st
	}
	for _, st := range a {
		other, ok := bm[st.Path]
		if !ok || other.Present != st.Present ||
			other.Kind != st.Kind ||
			other.Mode != st.Mode ||
			other.Skipped != st.Skipped ||
			other.LinkTarget != st.LinkTarget ||
			!bytes.Equal(other.Content, st.Content) {
			return false
		}
	}
	return true
}
