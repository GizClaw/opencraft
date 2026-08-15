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
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
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

// Store persists conversations under a root directory. It is safe for
// concurrent use.
type Store struct {
	root   string
	window int
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
	return &Store{root: root, window: window}, nil
}

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

// AppendTurn persists one turn. Only text and reasoning parts are
// archived; other modalities (tool calls, images, …) are dropped so
// the archive stays a compact conversation transcript.
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
		Messages []message.Message `json:"messages"`
	}{Seq: seq, Messages: archived}
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
// n <= 0 uses the store window.
func (s *Store) History(_ context.Context, id string, n int) ([]message.Message, error) {
	if n <= 0 {
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
	if len(all) > n {
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
		hist, _ := s.History(context.Background(), id, 0)
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
		}
		// Title: first user message; UpdatedAt: last message time.
		for _, h := range hist {
			if h.Role == message.RoleUser && m.Title == "" {
				m.Title = firstLine(h.Content.Text())
			}
			if h.Content.Text() != "" {
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

func (s *Store) nextSeq(historyDir string) (int, error) {
	files, err := filepath.Glob(filepath.Join(historyDir, "*.json"))
	if err != nil {
		return 0, err
	}
	return len(files) + 1, nil
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
