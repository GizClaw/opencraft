package assembly

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	toolmiddleware "github.com/GizClaw/flowcraft/core/tool/middleware"
)

func auditRecord(tool, args, result string) toolmiddleware.AuditRecord {
	return toolmiddleware.AuditRecord{
		Call: message.ToolCall{
			ID:        "call-1",
			Name:      tool,
			Arguments: json.RawMessage(args),
		},
		Result: message.ToolResult{
			CallID:  "call-1",
			Content: message.NewTextContent(result),
		},
		Duration: 1500 * time.Millisecond,
	}
}

func readAuditLines(t *testing.T, path string) []auditEntry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var out []auditEntry
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		var entry auditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode audit line %q: %v", line, err)
		}
		out = append(out, entry)
	}
	return out
}

// TestAuditSinkSkipsInternalPayloads pins the two bounds that keep a long
// turn's audit trail small: the internal compaction tool records sizes
// instead of the conversation it folds, and any other oversized field is
// truncated to the per-field cap.
func TestAuditSinkSkipsInternalPayloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "tool-calls.jsonl")
	sink := newFileAuditSink(path)
	ctx := context.Background()

	huge := strings.Repeat("x", 2<<20)
	sink.Record(ctx, auditRecord("compact", huge, "summary"))
	sink.Record(ctx, auditRecord("write_file", huge, huge))

	entries := readAuditLines(t, path)
	if len(entries) != 2 {
		t.Fatalf("audit entries = %d, want 2", len(entries))
	}
	compact := entries[0]
	if !compact.SkippedPayload || compact.Arguments != "" ||
		compact.Result != "" || compact.ArgsBytes != len(huge) {
		t.Fatalf("compact record = %+v", compact)
	}
	if compact.DurationMS != 1500 {
		t.Fatalf("duration = %d, want 1500", compact.DurationMS)
	}
	write := entries[1]
	if write.SkippedPayload || !write.Truncated {
		t.Fatalf("write record = %+v", write)
	}
	if len([]rune(write.Arguments)) > maxAuditFieldRunes+1 {
		t.Fatalf("arguments kept %d runes", len([]rune(write.Arguments)))
	}
	if write.ArgsBytes != len(huge) || write.ResultBytes != len(huge) {
		t.Fatalf("sizes = %d/%d, want %d", write.ArgsBytes, write.ResultBytes, len(huge))
	}
}

func TestAuditSinkRotatesAtTheCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool-calls.jsonl")
	sink := newFileAuditSink(path)
	ctx := context.Background()

	previous := maxAuditMB
	maxAuditMB = 1
	defer func() { maxAuditMB = previous }()

	// Fields are capped at maxAuditFieldRunes, so it takes a run of records
	// to reach 1 MiB; the write after that rotates the trail aside.
	for i := 0; i < 200; i++ {
		sink.Record(ctx, auditRecord("exec_command",
			"{"+strings.Repeat("a", 600<<10)+"}", "ok"))
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated generation missing: %v", err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("stat current trail: %v", err)
	} else if info.Size() >= int64(maxAuditMB)<<20 {
		t.Fatalf("current trail %d bytes, want under the cap", info.Size())
	}
}
