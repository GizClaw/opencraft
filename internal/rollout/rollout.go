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
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// maxRolloutMB bounds one rollout file (128 MiB). The file is rotated
// through the same lumberjack library the app log uses
// (flowcraft/telemetry/logfile wraps it), so long-running conversations
// cannot grow a single JSONL without bound. One previous generation is
// retained. It is a variable so tests can exercise rotation with small
// files.
var maxRolloutMB = 128

// rolloutBackups is the number of rotated rollout generations kept
// (lumberjack MaxBackups).
const rolloutBackups = 1

// tailBytes is how much of the file tail is scanned on reopen to
// recover the last sequence number without reading the whole file.
const tailBytes = 64 << 10

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
	lj   *lumberjack.Logger
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
	seq, err := tailSeq(path)
	if err != nil {
		return nil, err
	}
	sizeMB := maxRolloutMB
	if sizeMB <= 0 {
		sizeMB = 128
	}
	return &Recorder{
		path: path,
		seq:  seq,
		lj: &lumberjack.Logger{
			Filename:   path,
			MaxSize:    sizeMB,
			MaxBackups: rolloutBackups,
			LocalTime:  false,
		},
	}, nil
}

// Record marshals and appends one event line, stamping seq and time
// when empty.
func (r *Recorder) Record(ev Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ev.Time == "" {
		ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	ev.Seq = r.seq
	r.seq++
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = r.lj.Write(data)
	return err
}

// Close flushes and closes the underlying file.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lj.Close()
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

// tailSeq recovers the next sequence number (one past the last
// recorded seq) by reading only the tail of the file, falling back to
// a full line count when the last line cannot be parsed from the tail
// window.
func tailSeq(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if st.Size() == 0 {
		return 0, nil
	}
	off := st.Size() - tailBytes
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return 0, err
	}
	trimmed := bytes.TrimRight(buf, "\n")
	if len(trimmed) == 0 {
		return countLines(path)
	}
	idx := bytes.LastIndexByte(trimmed, '\n')
	line := trimmed[idx+1:]
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return countLines(path)
	}
	var last struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal(line, &last); err != nil {
		// The last event may straddle the tail boundary or be
		// malformed; a full count is the safe fallback.
		return countLines(path)
	}
	return last.Seq + 1, nil
}
