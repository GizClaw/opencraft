package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	coretelemetry "github.com/GizClaw/flowcraft/core/telemetry"
)

// auditFile is the app-level audit trail for the OTLP export sink. The
// sink is application-wide, so its trail lives next to the app rather
// than in a workspace audit directory.
const auditFile = "telemetry.jsonl"

// Audit actions recorded for the export sink.
const (
	// AuditInstall records a sink that became active.
	AuditInstall = "install"
	// AuditRemove records a sink that was dropped.
	AuditRemove = "remove"
	// AuditDeny records a configure request the host refused. Denials
	// are the interesting half of the trail: they show a plugin trying
	// to export without the permission or over another owner.
	AuditDeny = "deny"
)

// AuditEntry is one observable change to the OTLP export sink. Header
// values never appear here: they are credentials, so the entry carries
// the header names only.
type AuditEntry struct {
	Timestamp   string   `json:"timestamp"`
	Action      string   `json:"action"`
	PluginID    string   `json:"plugin_id,omitempty"`
	Endpoint    string   `json:"endpoint,omitempty"`
	Insecure    bool     `json:"insecure,omitempty"`
	HeaderNames []string `json:"header_names,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// AppendAudit appends one entry to <dir>/telemetry.jsonl. Audit writes
// are best-effort: a failure is reported through the log and never
// propagates to the caller, matching the rollout/usage rule that
// observability must not block the operation it observes.
func AppendAudit(dir string, entry AuditEntry) {
	if dir == "" {
		return
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		coretelemetry.WarnErr(context.Background(),
			"telemetry: marshal sink audit record failed", err)
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		coretelemetry.WarnErr(context.Background(),
			"telemetry: create sink audit dir failed", err)
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, auditFile),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		coretelemetry.WarnErr(context.Background(),
			"telemetry: open sink audit file failed", err)
		return
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		coretelemetry.WarnErr(context.Background(),
			"telemetry: append sink audit record failed", err)
	}
	if err := f.Close(); err != nil {
		coretelemetry.WarnErr(context.Background(),
			"telemetry: close sink audit file failed", err)
	}
}
