// Package sessions implements opencraft's project-scoped conversation
// store. Every conversation gets a random id and its full text +
// reasoning history is persisted under
// <project>/.opencraft/sessions/<id>/history/ as JSON files.
package sessions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/sessions/state"
)

// ResourceKind is the deploy resource kind implemented by this package.
const ResourceKind = "session.Store"

// Meta describes one stored conversation for the /resume list.
type Meta struct {
	ID        string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Messages  int
	Usage     Usage
}

// Usage is the cumulative token usage recorded for one session.
type Usage struct {
	Model            string `json:"model,omitempty"`
	InputTokens      int64  `json:"input_tokens,omitempty"`
	OutputTokens     int64  `json:"output_tokens,omitempty"`
	TotalTokens      int64  `json:"total_tokens,omitempty"`
	CacheReadTokens  int64  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64  `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64  `json:"reasoning_tokens,omitempty"`
	LatencyMs        int64  `json:"latency_ms,omitempty"`
}

// Store is the project's single session store: per-session JSON state
// (history, usage, permissions, plans) under <root>/<sid>/, plus the
// SQLite database at <root>/session.db that owns the conversation
// state tables and agent checkpoints. It is safe for concurrent use.
type Store struct {
	root   string
	window int
	db     *state.Store

	// mu guards seqCache (per-session next-turn sequence numbers).
	// The cache avoids globbing the whole history directory on every
	// append; it is seeded once per session from the existing files.
	// It also guards artifactBuf, the per-conversation artifact buffer
	// merged into the next archived turn by AppendTurn.
	mu          sync.Mutex
	seqCache    map[string]int
	artifactBuf map[string][]Artifact
	// turnTiming keeps the request/start timestamps of live runs keyed
	// by conversation then run id, so the commit/archive hook that later
	// persists the turn can attach the times the UI shows.
	turnTiming map[string]map[string]TurnTiming
	// importPending tracks imports that have written their history but
	// have not yet completed the memory seed. It is keyed by session id
	// (value = source key) so a concurrent duplicate import is rejected
	// instead of deleting an in-flight session.
	importPending map[string]string
}

// Artifact is one file produced by a turn, reported by the workspace
// observer and persisted with the turn archive.
type Artifact struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes,omitempty"`
}

// TurnRecord is one archived turn: its messages plus the artifacts the
// turn produced.
type TurnRecord struct {
	Seq         int               `json:"seq"`
	At          time.Time         `json:"at"`
	RequestedAt time.Time         `json:"requested_at,omitzero"`
	StartedAt   time.Time         `json:"started_at,omitzero"`
	FinishedAt  time.Time         `json:"finished_at,omitzero"`
	RunID       string            `json:"run_id,omitempty"`
	Messages    []message.Message `json:"messages"`
	Artifacts   []Artifact        `json:"artifacts,omitempty"`
}

// TurnTiming carries the timestamps one turn should display: when the
// user's request was received, when the agent execution started, and
// when it finished.
type TurnTiming struct {
	RequestedAt time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
}

// sessionMeta is the per-session index document (meta.json). It embeds
// token usage and the counters and timestamps that let the resume list
// and title generation avoid re-reading every history file.
type sessionMeta struct {
	Usage
	TurnCount    int       `json:"turn_count,omitempty"`
	MessageCount int       `json:"message_count,omitempty"`
	Title        string    `json:"title,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
	// ImportSource is the stable external key used for idempotent
	// session imports. ImportReady flips only after the caller has
	// seeded memory; the session list hides pending imports.
	ImportSource string `json:"import_source,omitempty"`
	ImportReady  bool   `json:"import_ready,omitempty"`
}

// New creates a Store rooted at root. The window is the number of
// recent messages injected into the model context.
func New(root string, window int) (*Store, error) {
	if root == "" {
		return nil, errdefs.Validationf("session store: root is required")
	}
	if window <= 0 {
		window = 40
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	// Session archives hold file contents and tool output, so the
	// directory stays owner-only.
	_ = os.Chmod(root, 0o700)
	db, err := state.Open(filepath.Join(root, "session.db"))
	if err != nil {
		return nil, err
	}
	return &Store{
		root:          root,
		window:        window,
		db:            db,
		seqCache:      make(map[string]int),
		importPending: make(map[string]string),
	}, nil
}

// State returns the SQLite store backing this session store (the
// conversation state tables and agent checkpoints).
func (s *Store) State() *state.Store { return s.db }

// Close closes the SQLite database handle.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Save implements agent.CheckpointStore (delegating to the SQLite
// store, which is the single checkpoint owner).
func (s *Store) Save(ctx context.Context, cp agent.Checkpoint) error {
	return s.db.Save(ctx, cp)
}

// Load implements agent.CheckpointStore.
func (s *Store) Load(ctx context.Context, execID string) (*agent.Checkpoint, error) {
	return s.db.Load(ctx, execID)
}

// Delete implements agent.CheckpointDeleter.
func (s *Store) Delete(ctx context.Context, execID string) error {
	return s.db.Delete(ctx, execID)
}

var _ agent.CheckpointStore = (*Store)(nil)
var _ agent.CheckpointDeleter = (*Store)(nil)

// NewID returns a fresh random session id.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "s-" + hex.EncodeToString(b[:])
}

// Exists reports whether the session's directory exists. It returns
// false for malformed ids and for sessions that were never created.
func (s *Store) Exists(id string) bool {
	if err := requireID(id); err != nil {
		return false
	}
	info, err := os.Stat(s.dir(id))
	return err == nil && info.IsDir()
}

// DefaultSessionID is the stable session key used by tools that run
// outside any conversation (no RunInfo in the execution context), e.g.
// update_plan's shared plan. It is a valid session id, so it can be
// passed to every Store method that resolves id against the store
// root.
const DefaultSessionID = "s-default"

// maxAttachmentBytes caps one user attachment copied into the session
// (media previews and file attachments). 10 MiB covers typical
// screenshots / photos while keeping session directories bounded.
const maxAttachmentBytes = 10 << 20

// Create makes a fresh conversation and returns its id.
func (s *Store) Create() (string, error) {
	id := NewID()
	dir := s.dir(id)
	// session ids are always valid here (freshly generated).
	if err := os.MkdirAll(filepath.Join(dir, "history"), 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	_ = os.Chmod(filepath.Join(dir, "history"), 0o700)
	return id, nil
}

// AppendTurn persists one turn. Text, reasoning, tool call, tool
// result, and media (image/audio/video/file) parts are archived;
// media sources are kept in URL form (attachments live in this
// session's media/ and files/ directories), so the archive stays a
// compact transcript while /resume can still re-render attachments.
func (s *Store) AppendTurn(_ context.Context, id string, msgs []message.Message) error {
	return s.appendTurn(id, "", msgs)
}

// AppendTurnWithRunID persists one turn like AppendTurn, additionally
// recording the run id so post-turn reconciliation (and any later
// correction) can target exactly this turn instead of guessing "the
// latest one".
func (s *Store) AppendTurnWithRunID(
	_ context.Context, id, runID string, msgs []message.Message,
) error {
	return s.appendTurn(id, runID, msgs)
}

func (s *Store) appendTurn(id, runID string, msgs []message.Message) error {
	if err := requireID(id); err != nil {
		return err
	}
	dir := s.dir(id)
	historyDir := filepath.Join(dir, "history")
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(historyDir, 0o700)
	seq, err := s.nextSeq(id, historyDir)
	if err != nil {
		return err
	}
	archived := filterArchive(msgs)
	if len(archived) == 0 {
		return nil
	}
	now := time.Now().UTC()
	timing := TurnTiming{RequestedAt: now, StartedAt: now}
	if runID != "" {
		if recorded, ok := s.takeTurnTiming(id, runID); ok {
			timing = recorded
		}
	}
	file := TurnRecord{
		Seq:         seq,
		At:          now,
		RequestedAt: timing.RequestedAt,
		StartedAt:   timing.StartedAt,
		FinishedAt:  timing.FinishedAt,
		RunID:       runID,
		Messages:    archived,
		Artifacts:   s.takeArtifacts(id),
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(historyDir, fmt.Sprintf("%06d.json", seq))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if runID != "" {
		s.indexRun(id, runID, path)
	}

	// Keep the meta index in sync: title (first user message), message
	// and turn counts, and timestamps. This is what makes List() and
	// auto-title O(1) instead of re-reading the whole archive.
	meta, err := s.loadMeta(id)
	if err != nil {
		return err
	}
	meta.TurnCount++
	meta.MessageCount += len(archived)
	if meta.Title == "" {
		for _, m := range archived {
			if m.Role == message.RoleUser {
				title := firstLine(m.Content.Text())
				if title == "" && len(m.Content.Parts) > 0 {
					// Media-only first message: still give the resume
					// list a name instead of an empty title.
					title = "[attachment]"
				}
				meta.Title = title
				break
			}
		}
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.UpdatedAt = now
	return s.writeMeta(id, meta)
}

// RecordTurnTiming stores the request and execution-start timestamps
// for a run that has not been archived yet. appendTurn consumes the
// entry when it persists the matching run, so live and resumed turns
// show the same times.
func (s *Store) RecordTurnTiming(
	id, runID string, requestedAt, startedAt time.Time,
) error {
	if err := requireID(id); err != nil {
		return err
	}
	if runID == "" {
		return errdefs.Validationf("sessions: timing run id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnTiming == nil {
		s.turnTiming = make(map[string]map[string]TurnTiming)
	}
	if s.turnTiming[id] == nil {
		s.turnTiming[id] = make(map[string]TurnTiming)
	}
	s.turnTiming[id][runID] = TurnTiming{
		RequestedAt: requestedAt.UTC(),
		StartedAt:   startedAt.UTC(),
	}
	return nil
}

// RecordTurnFinished records when a run finished. Completed turns are
// normally archived before waitTurn observes the result, so the method
// updates the matching turn file; when the archive has not been written
// yet it keeps the timestamp for the upcoming appendTurn call.
func (s *Store) RecordTurnFinished(
	id, runID string, finishedAt time.Time,
) error {
	if err := requireID(id); err != nil {
		return err
	}
	if runID == "" {
		return errdefs.Validationf("sessions: timing run id is required")
	}
	path, err := s.turnPathByRun(id, runID)
	if err != nil {
		return err
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var turn TurnRecord
		if err := json.Unmarshal(data, &turn); err != nil {
			return err
		}
		turn.FinishedAt = finishedAt.UTC()
		out, err := json.MarshalIndent(turn, "", "  ")
		if err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, out, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnTiming == nil {
		s.turnTiming = make(map[string]map[string]TurnTiming)
	}
	if s.turnTiming[id] == nil {
		s.turnTiming[id] = make(map[string]TurnTiming)
	}
	timing := s.turnTiming[id][runID]
	timing.FinishedAt = finishedAt.UTC()
	s.turnTiming[id][runID] = timing
	return nil
}

func (s *Store) takeTurnTiming(id, runID string) (TurnTiming, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnTiming == nil {
		return TurnTiming{}, false
	}
	timing, ok := s.turnTiming[id][runID]
	if ok {
		delete(s.turnTiming[id], runID)
		if len(s.turnTiming[id]) == 0 {
			delete(s.turnTiming, id)
		}
	}
	return timing, ok
}

// BufferArtifact records one produced file for the conversation's next
// archived turn. The artifact is attached to the turn file by
// AppendTurn (the commit/archive hooks) and cleared afterwards, so
// interrupted turns keep their partial outputs too. Re-writing the
// same path refreshes its byte count in place.
func (s *Store) BufferArtifact(id, path string, bytes int) error {
	if err := requireID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.artifactBuf == nil {
		s.artifactBuf = make(map[string][]Artifact)
	}
	list := s.artifactBuf[id]
	for i := range list {
		if list[i].Path == path {
			list[i].Bytes = bytes
			return nil
		}
	}
	s.artifactBuf[id] = append(list, Artifact{Path: path, Bytes: bytes})
	return nil
}

// takeArtifacts returns and clears the buffered artifacts for id.
func (s *Store) takeArtifacts(id string) []Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.artifactBuf[id]
	delete(s.artifactBuf, id)
	return list
}

// AppendTurnArtifacts merges artifacts into the archived turn that
// carried runID (falling back to the most recent turn), deduping by
// path: a repeat refresh keeps the first-seen order and the latest
// byte count. It returns the turn's merged artifact list, which the
// caller can push to the UI as the authoritative post-reconciliation
// set. A conversation without an archived turn is a no-op (nil, nil).
// It is used for post-turn reconciliation: files produced outside the
// workspace API (e.g. exec writing docs directly) become visible only
// after the turn archive was written.
func (s *Store) AppendTurnArtifacts(
	id, runID string, artifacts []Artifact,
) ([]Artifact, error) {
	if err := requireID(id); err != nil {
		return nil, err
	}
	if len(artifacts) == 0 {
		return nil, nil
	}
	path, err := s.turnPathByRun(id, runID)
	if err != nil || path == "" {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var turn TurnRecord
	if err := json.Unmarshal(data, &turn); err != nil {
		return nil, err
	}
	for _, artifact := range artifacts {
		idx := -1
		for i := range turn.Artifacts {
			if turn.Artifacts[i].Path == artifact.Path {
				idx = i
				break
			}
		}
		if idx >= 0 {
			turn.Artifacts[idx].Bytes = artifact.Bytes
			continue
		}
		turn.Artifacts = append(turn.Artifacts, artifact)
	}
	out, err := json.MarshalIndent(turn, "", "  ")
	if err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return turn.Artifacts, nil
}

// turnPathByRun returns the archived turn file whose run id matches
// runID, or the highest-numbered file when runID is empty.
func (s *Store) turnPathByRun(id, runID string) (string, error) {
	if runID != "" {
		idx, _ := s.readRunIndex(id)
		if name, ok := idx[runID]; ok {
			candidate := filepath.Join(s.dir(id), "history", name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
		return "", nil
	}
	files, err := filepath.Glob(filepath.Join(s.dir(id), "history", "*.json"))
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", nil
	}
	sort.Strings(files)
	return files[len(files)-1], nil
}

// runIndexFile maps one turn's run id to its archived file name, so
// post-turn artifact reconciliation is O(1) instead of re-reading every
// history file on each turn.
const runIndexFile = "runs.json"

func (s *Store) indexRun(id, runID, path string) {
	idx, err := s.readRunIndex(id)
	if err != nil {
		idx = map[string]string{}
	}
	idx[runID] = filepath.Base(path)
	_ = s.writeRunIndex(id, idx)
}

func (s *Store) readRunIndex(id string) (map[string]string, error) {
	idx := map[string]string{}
	data, err := os.ReadFile(filepath.Join(s.dir(id), runIndexFile))
	if err != nil {
		if os.IsNotExist(err) {
			return idx, nil
		}
		return idx, err
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return idx, err
	}
	return idx, nil
}

func (s *Store) writeRunIndex(id string, idx map[string]string) error {
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir(id), runIndexFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SaveAttachment copies one user attachment into the session's media
// or files directory and returns the stored absolute path. opencraft
// currently copies only images into "media" (resume rendering needs
// the bytes); audio/video/file attachments keep their original paths.
// The copy keeps the source extension so media type detection survives
// the rename; the name carries a random suffix so repeated uploads of
// the same file never collide.
func (s *Store) SaveAttachment(id, kind, srcPath string) (string, error) {
	if err := requireID(id); err != nil {
		return "", err
	}
	if kind != "media" && kind != "files" {
		return "", errdefs.Validationf("sessions: unknown attachment kind %q", kind)
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errdefs.Validationf("sessions: attachment is not a regular file")
	}
	if info.Size() > maxAttachmentBytes {
		return "", errdefs.Validationf(
			"sessions: attachment too large (%d bytes)", info.Size())
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()
	dir := filepath.Join(s.dir(id), kind)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	name := fmt.Sprintf("%d-%x%s", time.Now().UnixNano(), suffix[:], filepath.Ext(srcPath))
	dst := filepath.Join(dir, name)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return "", err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	return dst, nil
}

// History returns the most recent n archived messages, oldest first.
// n < 0 returns every archived message; n == 0 uses the store window
// (the recent context injected into the model); n > 0 caps at n.
func (s *Store) History(_ context.Context, id string, n int) ([]message.Message, error) {
	if err := requireID(id); err != nil {
		return nil, err
	}
	if n == 0 {
		n = s.window
	}
	files, err := filepath.Glob(filepath.Join(s.dir(id), "history", "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	// The bounded window only reads the newest files that can supply n
	// messages; a full history (n < 0) still reads everything.
	limit := n
	if limit < 0 {
		limit = int(^uint(0) >> 1)
	}
	var all []message.Message
	for i := len(files) - 1; i >= 0 && len(all) < limit; i-- {
		path := files[i]
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var turn struct {
			Messages []message.Message `json:"messages"`
		}
		if json.Unmarshal(data, &turn) != nil {
			continue
		}
		all = append(turn.Messages, all...)
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// Turns returns every archived turn of one conversation, oldest first,
// including each turn's produced artifacts.
func (s *Store) Turns(_ context.Context, id string) ([]TurnRecord, error) {
	if err := requireID(id); err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(s.dir(id), "history", "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	turns := make([]TurnRecord, 0, len(files))
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var turn TurnRecord
		if json.Unmarshal(data, &turn) != nil {
			continue
		}
		turns = append(turns, turn)
	}
	return turns, nil
}

// RolloutPath returns the JSONL rollout path for one conversation.
// The file is append-only event stream owned by the rollout recorder.
func (s *Store) RolloutPath(id string) (string, error) {
	if err := requireID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.dir(id), "rollout.jsonl"), nil
}

// List returns metadata for every conversation in the store, newest
// first.
func (s *Store) List() ([]Meta, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			// First launch: no session directory yet means an empty
			// list, not null (the desktop UI iterates the result).
			return []Meta{}, nil
		}
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "s-") {
			continue
		}
		id := e.Name()
		meta, err := s.loadMeta(id)
		if err != nil {
			continue
		}
		if meta.ImportSource != "" && !meta.ImportReady {
			// The import is still seeding memory (or was interrupted
			// before completion); keep it out of the resume list.
			continue
		}
		if meta.TurnCount == 0 && meta.Usage == (Usage{}) {
			continue
		}
		m := Meta{
			ID:    id,
			Title: meta.Title,
			Usage: meta.Usage,
		}
		m.Messages = meta.MessageCount
		if m.Messages == 0 {
			m.Messages = meta.TurnCount
		}
		if !meta.CreatedAt.IsZero() {
			m.CreatedAt = meta.CreatedAt
		}
		if !meta.UpdatedAt.IsZero() {
			m.UpdatedAt = meta.UpdatedAt
		}
		if info, err := e.Info(); err == nil {
			if m.CreatedAt.IsZero() {
				m.CreatedAt = info.ModTime()
			}
			if m.UpdatedAt.IsZero() {
				m.UpdatedAt = info.ModTime()
			}
		}
		if m.Title == "" {
			m.Title = "(empty)"
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// Title returns the archived conversation title from the meta index.
func (s *Store) Title(id string) (string, error) {
	if err := requireID(id); err != nil {
		return "", err
	}
	meta, err := s.loadMeta(id)
	if err != nil {
		return "", err
	}
	if meta.Title != "" {
		return meta.Title, nil
	}
	return "", nil
}

// FirstUserMessage returns the archived first non-empty user message
// text. It reads only the oldest turn files (bounded), so auto-title
// generation never pays for the full archive of a long session.
func (s *Store) FirstUserMessage(id string) (string, error) {
	if err := requireID(id); err != nil {
		return "", err
	}
	files, err := filepath.Glob(filepath.Join(s.dir(id), "history", "*.json"))
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	for i := 0; i < len(files) && i < 10; i++ {
		data, err := os.ReadFile(files[i])
		if err != nil {
			continue
		}
		var turn struct {
			Messages []message.Message `json:"messages"`
		}
		if json.Unmarshal(data, &turn) != nil {
			continue
		}
		for _, m := range turn.Messages {
			if m.Role != message.RoleUser {
				continue
			}
			if text := strings.TrimSpace(m.Content.Text()); text != "" {
				return text, nil
			}
		}
	}
	return "", nil
}

// loadMeta reads the per-session index document. A missing file is a
// zero document.
func (s *Store) loadMeta(id string) (sessionMeta, error) {
	var meta sessionMeta
	data, err := os.ReadFile(filepath.Join(s.dir(id), "meta.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return meta, nil
		}
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

// writeMeta persists the per-session index document atomically with
// owner-only permissions.
func (s *Store) writeMeta(id string, meta sessionMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	dir := s.dir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	path := filepath.Join(dir, "meta.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadUsage returns the cumulative token usage recorded for a session.
func (s *Store) LoadUsage(_ context.Context, id string) (Usage, error) {
	if err := requireID(id); err != nil {
		return Usage{}, err
	}
	meta, err := s.loadMeta(id)
	if err != nil {
		return Usage{}, err
	}
	return meta.Usage, nil
}

// RecordUsage persists the cumulative token usage for a session. The
// caller supplies the full session totals; it is written atomically.
func (s *Store) RecordUsage(_ context.Context, id string, usage Usage) error {
	if err := requireID(id); err != nil {
		return err
	}
	meta, err := s.loadMeta(id)
	if err != nil {
		return err
	}
	meta.Usage = usage
	return s.writeMeta(id, meta)
}

func (s *Store) dir(id string) string {
	return filepath.Join(s.root, id)
}

// Remove deletes one stored conversation end to end: its directory
// (history, permissions, usage), its SQLite memory rows (items and
// summary nodes) and its session_settings row (think level, model
// hint). The id must be a generated conversation id; runtime checkpoint
// state for the same id is removed by the session manager's
// DeleteSession (the desktop wires both together).
func (s *Store) Remove(ctx context.Context, id string) error {
	if err := requireID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeLocked(ctx, id)
}

// removeLocked removes one conversation while the caller holds s.mu.
func (s *Store) removeLocked(ctx context.Context, id string) error {
	delete(s.seqCache, id)
	delete(s.importPending, id)
	delete(s.turnTiming, id)
	if err := os.RemoveAll(s.dir(id)); err != nil {
		return fmt.Errorf("sessions: remove %s: %w", id, err)
	}
	if err := s.db.DeleteThread(ctx, id); err != nil {
		return fmt.Errorf("sessions: remove %s memory: %w", id, err)
	}
	if err := s.db.RemoveSettings(ctx, id); err != nil {
		return fmt.Errorf("sessions: remove settings %s: %w", id, err)
	}
	return nil
}

// filterArchive keeps the parts the session archive understands and
// drops messages that end up with no archiveable content.
func filterArchive(msgs []message.Message) []message.Message {
	var archived []message.Message
	for _, m := range msgs {
		var parts []message.Part
		for _, p := range m.Content.Parts {
			switch part := p.(type) {
			case message.TextPart:
				parts = append(parts, part)
			case message.ReasoningPart:
				parts = append(parts, part)
			case message.ToolCallPart:
				parts = append(parts, part)
			case message.ToolResultPart:
				parts = append(parts, part)
			case message.ImagePart:
				parts = append(parts, part)
			case message.AudioPart:
				parts = append(parts, part)
			case message.VideoPart:
				parts = append(parts, part)
			case message.FilePart:
				parts = append(parts, part)
			case message.DataPart:
				parts = append(parts, part)
			}
		}
		if len(parts) > 0 {
			archived = append(archived, message.Message{
				Role:    m.Role,
				Content: message.Content{Parts: parts},
			})
		}
	}
	return archived
}

// requireID validates a caller-supplied session id before it becomes a
// filesystem path. Every Store method that resolves id against the
// store root checks it here, so no binding can bypass validation by
// calling a lower-level method directly.
func requireID(id string) error {
	if !ValidID(id) {
		return errdefs.Validationf("sessions: invalid session id %q", id)
	}
	return nil
}

// ValidID reports whether id is a safe conversation id: it must carry
// the "s-" prefix and contain no path separators, so
// filepath.Join(s.root, id) can never resolve outside s.root even
// through Clean (crafted ids like "s-../../../../tmp/x" are rejected).
// Store methods that resolve id against the store root enforce this via
// requireID; bindings may also check it up front for a clearer error.
func ValidID(id string) bool {
	if id == "" || !strings.HasPrefix(id, "s-") {
		return false
	}
	return !strings.ContainsAny(id, `/\`)
}

// nextSeq returns the next turn sequence for one session. The value is
// cached in memory after the first scan, so a long-lived session never
// globs its growing history directory on every append.
func (s *Store) nextSeq(id, historyDir string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if next, ok := s.seqCache[id]; ok {
		s.seqCache[id] = next + 1
		return next, nil
	}
	max, err := scanSeq(historyDir)
	if err != nil {
		return 0, err
	}
	s.seqCache[id] = max + 2
	return max + 1, nil
}

// scanSeq derives the next sequence from the largest archived file
// name rather than the file count: the archive is append-only today,
// but a count-based seq would collide after any future cleanup or
// compaction.
func scanSeq(historyDir string) (int, error) {
	files, err := filepath.Glob(filepath.Join(historyDir, "*.json"))
	if err != nil {
		return 0, err
	}
	max := 0
	for _, path := range files {
		n, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(path), ".json"))
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	return max, nil
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}

// ---------- deploy resource ----------

// Factory builds the session store resource from deploy settings.
type Factory struct{}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: "opencraft"}
}

type settings struct {
	Root   string `json:"root"`
	Window int    `json:"window,omitempty"`
}

// New implements resource.Factory.
func (Factory) New(ctx context.Context, in resource.Input) (any, error) {
	s, err := resource.DecodeTyped[settings](ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf("session store: %v", err)
	}
	return New(s.Root, s.Window)
}

// Register adds the session store factory to r.
func Register(r *resource.Registry) error {
	return r.Register(Factory{})
}
