package assembly

import (
	"context"
	"encoding/json"
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
	sink := &fileAuditSink{path: filepath.Join(s.Dir, "tool-calls.jsonl")}
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
	Arguments  string `json:"arguments"`
	Result     string `json:"result"`
	IsError    bool   `json:"is_error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// fileAuditSink appends records to a single JSONL file. It is safe for
// concurrent use and never breaks execution: failures are logged and
// dropped, matching the audit middleware's contract.
type fileAuditSink struct {
	mu   sync.Mutex
	path string
}

// Record implements toolmiddleware.AuditSink.
func (s *fileAuditSink) Record(ctx context.Context, rec toolmiddleware.AuditRecord) {
	entry := auditEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Tool:       rec.Call.Name,
		CallID:     rec.Result.CallID,
		Arguments:  string(rec.Call.Arguments),
		Result:     rec.Result.Content,
		IsError:    rec.Result.IsError,
		DurationMS: rec.Duration.Milliseconds(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		telemetry.WarnErr(ctx, "opencraft audit: marshal record failed", err)
		return
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		telemetry.WarnErr(ctx, "opencraft audit: create directory failed", err,
			otellog.String("dir", filepath.Dir(s.path)))
		return
	}
	telemetry.WarnErr(ctx, "opencraft audit: secure audit directory failed",
		os.Chmod(filepath.Dir(s.path), 0o700),
		otellog.String("dir", filepath.Dir(s.path)))
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		telemetry.WarnErr(ctx, "opencraft audit: open audit file failed", err,
			otellog.String("path", s.path))
		return
	}
	defer func() {
		telemetry.WarnErr(ctx, "opencraft audit: close audit file failed",
			f.Close())
	}()
	if _, err := f.Write(data); err != nil {
		telemetry.WarnErr(ctx, "opencraft audit: write record failed", err,
			otellog.String("path", s.path))
	}
}

var _ toolmiddleware.AuditSink = (*fileAuditSink)(nil)
