// Package rollout implements the append-only JSONL session event
// stream: every conversation writes one rollout.jsonl whose lines are
// the thread/turn/item events in occurrence order. It is the durable
// "what actually happened" view that session snapshots cannot provide,
// and the future source for non-interactive exec --json output and
// app-server style protocols.
package rollout

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event types. Thread/turn events are recorded by the orchestrator;
// item events (tool calls, results, reasoning, assistant text) are
// synthesized from the stream.
const (
	TypeThreadStarted    = "thread.started"
	TypeTurnStarted      = "turn.started"
	TypeTurnCompleted    = "turn.completed"
	TypeTurnFailed       = "turn.failed"
	TypeItemToolCall     = "item.tool_call"
	TypeItemToolResult   = "item.tool_result"
	TypeItemReasoning    = "item.reasoning"
	TypeItemAssistantMsg = "item.assistant_message"
)

// Usage is the token accounting attached to turn.completed.
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
	LatencyMs       int64 `json:"latency_ms"`
}

// Event is one JSONL line. Fields are flat so every consumer can
// decode any line without a union schema; unused fields stay empty.
type Event struct {
	Type           string          `json:"type"`
	Seq            int64           `json:"seq"`
	Time           string          `json:"time"`
	ConversationID string          `json:"conversation_id,omitempty"`
	RunID          string          `json:"run_id,omitempty"`
	ItemID         string          `json:"item_id,omitempty"`
	Tool           string          `json:"tool,omitempty"`
	CallID         string          `json:"call_id,omitempty"`
	Arguments      json.RawMessage `json:"arguments,omitempty"`
	Content        string          `json:"content,omitempty"`
	IsError        bool            `json:"is_error,omitempty"`
	Status         string          `json:"status,omitempty"`
	Error          string          `json:"error,omitempty"`
	Usage          *Usage          `json:"usage,omitempty"`
}

// Recorder appends events to one conversation's rollout file. Safe for
// concurrent use; writes never block execution meaningfully.
type Recorder struct {
	mu   sync.Mutex
	f    *os.File
	path string
	seq  int64
}

// Open opens (creating if needed) the rollout file at path. Seq
// continues from the existing line count so records stay unique across
// restarts.
func Open(path string) (*Recorder, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	seq, err := countLines(path)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Recorder{f: f, path: path, seq: seq}, nil
}

// Record marshals and appends one event line, stamping seq and time
// when empty.
func (r *Recorder) Record(ev Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ev.Seq = r.seq
	r.seq++
	if ev.Time == "" {
		ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = r.f.Write(data)
	return err
}

// Close flushes and closes the underlying file.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

// Path returns the rollout file path.
func (r *Recorder) Path() string { return r.path }

func countLines(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	return int64(bytes.Count(data, []byte{'\n'})), nil
}
