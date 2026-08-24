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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	InputTokens      int64 `json:"input_tokens,omitempty"`
	OutputTokens     int64 `json:"output_tokens,omitempty"`
	TotalTokens      int64 `json:"total_tokens,omitempty"`
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
	LatencyMs        int64 `json:"latency_ms,omitempty"`
}

// Store is the project's single session store: per-session JSON state
// (history, usage, permissions, plans) under <root>/<sid>/, plus the
// SQLite database at <root>/session.db that owns the conversation
// state tables and agent checkpoints. It is safe for concurrent use.
type Store struct {
	root   string
	window int
	db     *state.Store
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
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	db, err := state.Open(filepath.Join(root, "session.db"))
	if err != nil {
		return nil, err
	}
	return &Store{root: root, window: window, db: db}, nil
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

// Create makes a fresh conversation and returns its id.
func (s *Store) Create() (string, error) {
	id := NewID()
	dir := s.dir(id)
	if err := os.MkdirAll(filepath.Join(dir, "history"), 0o755); err != nil {
		return "", err
	}
	return id, nil
}

// AppendTurn persists one turn. Text, reasoning, tool call, and tool
// result parts are archived; images/audio/data are dropped so the
// archive stays a compact conversation transcript. Keeping the
// structured tool parts lets /resume replay the live rendering path
// instead of parsing flattened text.
func (s *Store) AppendTurn(_ context.Context, id string, msgs []message.Message) error {
	dir := s.dir(id)
	historyDir := filepath.Join(dir, "history")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return err
	}
	seq, err := s.nextSeq(historyDir)
	if err != nil {
		return err
	}
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
			}
		}
		if len(parts) > 0 {
			archived = append(archived, message.Message{
				Role:    m.Role,
				Content: message.Content{Parts: parts},
			})
		}
	}
	if len(archived) == 0 {
		return nil
	}
	file := struct {
		Seq      int               `json:"seq"`
		At       time.Time         `json:"at"`
		Messages []message.Message `json:"messages"`
	}{Seq: seq, At: time.Now().UTC(), Messages: archived}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(historyDir, fmt.Sprintf("%06d.json", seq))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// History returns the most recent n archived messages, oldest first.
// n < 0 returns every archived message; n == 0 uses the store window
// (the recent context injected into the model); n > 0 caps at n.
func (s *Store) History(_ context.Context, id string, n int) ([]message.Message, error) {
	if n == 0 {
		n = s.window
	}
	var all []message.Message
	files, err := filepath.Glob(filepath.Join(s.dir(id), "history", "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	for _, path := range files {
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
		all = append(all, turn.Messages...)
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// List returns metadata for every conversation in the store, newest
// first.
func (s *Store) List() ([]Meta, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "s-") {
			continue
		}
		id := e.Name()
		hist, _ := s.History(context.Background(), id, -1)
		usage, _ := s.LoadUsage(context.Background(), id)
		if len(hist) == 0 && usage == (Usage{}) {
			continue
		}
		info, _ := e.Info()
		m := Meta{
			ID:       id,
			Messages: len(hist),
			Usage:    usage,
		}
		if info != nil {
			m.CreatedAt = info.ModTime()
			m.UpdatedAt = info.ModTime()
		}
		// Prefer the archived turn timestamps over the directory mtime:
		// the directory is touched on every append, so its mtime drifts
		// from the actual message times.
		if times := s.turnTimes(id); len(times) > 0 {
			m.CreatedAt = times[0]
			m.UpdatedAt = times[len(times)-1]
		}
		// Title: first user message.
		for _, h := range hist {
			if h.Role == message.RoleUser && m.Title == "" {
				m.Title = firstLine(h.Content.Text())
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

// LoadUsage returns the cumulative token usage recorded for a session.
func (s *Store) LoadUsage(_ context.Context, id string) (Usage, error) {
	var usage Usage
	data, err := os.ReadFile(filepath.Join(s.dir(id), "meta.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return usage, nil
		}
		return usage, err
	}
	if err := json.Unmarshal(data, &usage); err != nil {
		return usage, err
	}
	return usage, nil
}

// RecordUsage persists the cumulative token usage for a session. The
// caller supplies the full session totals; it is written atomically.
func (s *Store) RecordUsage(_ context.Context, id string, usage Usage) error {
	data, err := json.MarshalIndent(usage, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir(id), "meta.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) dir(id string) string {
	return filepath.Join(s.root, id)
}

// Remove deletes one stored conversation: its directory (history,
// permissions, usage) and its session_settings row (think level, model
// hint). The id must be a generated conversation id; runtime checkpoint
// state for the same id is removed by the session manager's
// DeleteSession (the desktop wires both together).
func (s *Store) Remove(id string) error {
	if !strings.HasPrefix(id, "s-") {
		return errdefs.Validationf("sessions: invalid session id %q", id)
	}
	if err := os.RemoveAll(s.dir(id)); err != nil {
		return fmt.Errorf("sessions: remove %s: %w", id, err)
	}
	if err := s.db.RemoveSettings(context.Background(), id); err != nil {
		return fmt.Errorf("sessions: remove settings %s: %w", id, err)
	}
	return nil
}

func (s *Store) nextSeq(historyDir string) (int, error) {
	files, err := filepath.Glob(filepath.Join(historyDir, "*.json"))
	if err != nil {
		return 0, err
	}
	// Derive the next sequence from the largest file name rather than the
	// file count: the archive is append-only today, but a count-based seq
	// would collide after any future cleanup or compaction.
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
	return max + 1, nil
}

// turnTimes returns the archived turn timestamps in seq order. Turns
// written before the "at" field existed (zero time) are skipped so old
// archives fall back to the directory mtime in List.
func (s *Store) turnTimes(id string) []time.Time {
	files, err := filepath.Glob(filepath.Join(s.dir(id), "history", "*.json"))
	if err != nil {
		return nil
	}
	sort.Strings(files)
	var out []time.Time
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var turn struct {
			At time.Time `json:"at"`
		}
		if json.Unmarshal(data, &turn) != nil || turn.At.IsZero() {
			continue
		}
		out = append(out, turn.At)
	}
	return out
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
func (Factory) New(_ context.Context, in resource.Input) (any, error) {
	s, err := resource.DecodeTyped[settings](in.Settings, resource.ExpandEnv())
	if err != nil {
		return nil, errdefs.Validationf("session store: %v", err)
	}
	return New(s.Root, s.Window)
}

// Register adds the session store factory to r.
func Register(r *resource.Registry) error {
	return r.Register(Factory{})
}
