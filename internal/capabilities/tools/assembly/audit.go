package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	toolmiddleware "github.com/GizClaw/flowcraft/core/tool/middleware"
	otellog "go.opentelemetry.io/otel/log"
)

// AuditSettings enables the append-only tool-call audit trail. Dir is
// the directory receiving tool-calls.jsonl; records carry redacted
// copies of arguments and results when redaction rules are configured.
type AuditSettings struct {
	Enabled bool   `json:"enabled"`
	Dir     string `json:"dir,omitempty"`
}

// maxAuditMB bounds one audit file; lumberjack rotates past it and keeps
// maxAuditBackups generations. A workspace that ran long turns wrote a
// single 500 MiB+ tool-calls.jsonl before this cap existed, because the
// internal compaction tool logs the conversation it folds.
var maxAuditMB = 64

const maxAuditBackups = 2

// maxAuditFieldRunes truncates one recorded argument/result field. The
// full text stays available in the rollout/session archive; the audit
// trail only has to be useful, not a second copy of every payload.
const maxAuditFieldRunes = 16 << 10

// payloadFreeTools lists internal bookkeeping tools whose arguments are
// the conversation itself: a single compaction fold was observed carrying
// 15 MiB. Their record keeps the fact, the sizes, and the duration, never
// the payload.
var payloadFreeTools = map[string]bool{"compact": true}

func auditMiddleware(
	s *AuditSettings,
	rules []toolmiddleware.RedactRule,
) (tool.Middleware, error) {
	if s == nil || !s.Enabled {
		return nil, nil
	}
	if s.Dir == "" {
		return nil, errdefs.Validationf(
			"tool middleware: audit.dir is required when audit.enabled is true")
	}
	sink := newFileAuditSink(filepath.Join(s.Dir, "tool-calls.jsonl"))
	if len(rules) > 0 {
		return toolmiddleware.AuditRedacted(sink, rules...), nil
	}
	return toolmiddleware.Audit(sink), nil
}

// auditEntry is one JSONL line in the audit trail.
type auditEntry struct {
	Timestamp  string `json:"timestamp"`
	Tool       string `json:"tool"`
	CallID     string `json:"call_id"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	// ArgsBytes/ResultBytes always carry the real payload sizes, even when
	// the text itself was skipped or truncated.
	ArgsBytes   int `json:"args_bytes,omitempty"`
	ResultBytes int `json:"result_bytes,omitempty"`
	// SkippedPayload marks a record whose arguments/result were dropped on
	// purpose (internal payload-free tools).
	SkippedPayload bool `json:"skipped_payload,omitempty"`
	// Truncated marks a field cut down to maxAuditFieldRunes.
	Truncated bool `json:"truncated,omitempty"`
}

// fileAuditSink appends records to a single rotating JSONL file. It is
// safe for concurrent use and never breaks execution: failures are logged
// and dropped, matching the audit middleware's contract.
//
// The file is opened per record on purpose: the sink has no lifecycle
// owner (a runtime rebuild replaces the assembly without telling the
// middleware), and holding the handle open would leak a descriptor per
// rebuild. Rotation replaces the previous generation, which is the same
// policy the rollout recorder applies.
type fileAuditSink struct {
	mu    sync.Mutex
	path  string
	dir   string
	dirOK bool
}

func newFileAuditSink(path string) *fileAuditSink {
	return &fileAuditSink{
		path: path,
		dir:  filepath.Dir(path),
	}
}

// truncateAuditField caps one recorded field and reports whether it was
// cut, so a record can say so instead of silently losing text.
func truncateAuditField(text string) (string, bool) {
	runes := []rune(text)
	if len(runes) <= maxAuditFieldRunes {
		return text, false
	}
	return string(runes[:maxAuditFieldRunes]) + "…", true
}

// Record implements toolmiddleware.AuditSink.
func (s *fileAuditSink) Record(ctx context.Context, rec toolmiddleware.AuditRecord) {
	entry := auditEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Tool:       rec.Call.Name,
		CallID:     rec.Result.CallID,
		IsError:    rec.Result.IsError,
		DurationMS: rec.Duration.Milliseconds(),
	}
	arguments := string(rec.Call.Arguments)
	// The audit trail is a text projection: media parts are recorded as
	// their kind only, never as inline bytes.
	result := rec.Result.Content.Text()
	entry.ArgsBytes = len(arguments)
	entry.ResultBytes = len(result)
	if payloadFreeTools[rec.Call.Name] {
		entry.SkippedPayload = true
	} else {
		args, argsCut := truncateAuditField(arguments)
		res, resCut := truncateAuditField(result)
		entry.Arguments = args
		entry.Result = res
		entry.Truncated = argsCut || resCut
	}
	data, err := json.Marshal(entry)
	if err != nil {
		telemetry.WarnErr(ctx, "opencraft audit: marshal record failed", err)
		return
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	// Directory preparation is once per sink, not once per tool call: the
	// per-record chmod was pure syscall overhead on every execution.
	if !s.dirOK {
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			telemetry.WarnErr(ctx, "opencraft audit: create directory failed", err,
				otellog.String("dir", s.dir))
			return
		}
		telemetry.WarnErr(ctx, "opencraft audit: secure audit directory failed",
			os.Chmod(s.dir, 0o700),
			otellog.String("dir", s.dir))
		s.dirOK = true
	}
	if err := s.write(data); err != nil {
		telemetry.WarnErr(ctx, "opencraft audit: write record failed", err,
			otellog.String("path", s.path))
	}
}

// write appends one line, rotating the trail aside first when it has
// reached the cap. Callers hold s.mu.
func (s *fileAuditSink) write(data []byte) error {
	if err := s.rotateLocked(); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"opencraft audit: close audit file failed", f.Close())
	}()
	_, err = f.Write(data)
	return err
}

// rotateLocked renames the trail to .1 once it passes maxAuditMB, keeping
// maxAuditBackups generations. Callers hold s.mu.
func (s *fileAuditSink) rotateLocked() error {
	sizeMB := maxAuditMB
	if sizeMB <= 0 {
		sizeMB = 64
	}
	info, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Size() < int64(sizeMB)<<20 {
		return nil
	}
	if maxAuditBackups <= 0 {
		return os.Truncate(s.path, 0)
	}
	// Rename replaces the oldest generation; Windows refuses an existing
	// target, so remove it first.
	oldest := s.backupPath(maxAuditBackups)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := maxAuditBackups - 1; i >= 1; i-- {
		if err := os.Rename(s.backupPath(i), s.backupPath(i+1)); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(s.path, s.backupPath(1))
}

func (s *fileAuditSink) backupPath(generation int) string {
	return fmt.Sprintf("%s.%d", s.path, generation)
}

var _ toolmiddleware.AuditSink = (*fileAuditSink)(nil)
