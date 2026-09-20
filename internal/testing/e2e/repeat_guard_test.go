package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/headless"
	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestHeadlessRepeatGuardStopsIdenticalCalls pins the L1-1 acceptance
// shape end to end: the model emits the same call three times, the
// repeat middleware from tools.yaml refuses the third one, the refusal
// reaches the model as an error result, and the run ends by the model's
// own decision in a bounded number of rounds instead of burning the run
// timeout on the same no-op.
func TestHeadlessRepeatGuardStopsIdenticalCalls(t *testing.T) {
	const args = `{"file_path":"out.txt","content":"hello e2e\n"}`
	identical := fakeprovider.Reply{
		ToolCalls: []fakeprovider.ToolCall{{Name: "write_file", Arguments: args}},
	}
	provider := fakeprovider.New(t,
		identical, // executed
		identical, // executed
		identical, // refused by the repeat guard
		fakeprovider.Reply{Text: "the same call is refused, stopping"},
	)
	workDir := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeInferenceConfig(t, configDir, provider.URL())

	var out bytes.Buffer
	res, err := headless.Run(context.Background(), headless.Options{
		WorkDir:   workDir,
		ConfigDir: configDir,
		Prompt:    "write out.txt",
		Out:       &out,
	})
	if err != nil {
		t.Fatalf("headless.Run: %v", err)
	}
	if res.Status != "completed" || res.ExitCode != 0 {
		t.Fatalf("result = %+v, want completed/0", res)
	}
	// One provider call per round: two executed calls, the refused one,
	// and the model's closing message. A guard that never fired would
	// still show up here as a third execution below.
	if provider.Calls() != 4 {
		t.Fatalf("provider calls = %d, want 4", provider.Calls())
	}

	var executed, refused int
	var refusal string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var ev rollout.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid JSONL event %q: %v", line, err)
		}
		if ev.Type != rollout.TypeItemToolResult {
			continue
		}
		if ev.IsError {
			refused++
			refusal = ev.Content
			continue
		}
		executed++
	}
	if executed != 2 || refused != 1 {
		t.Fatalf("tool results executed=%d refused=%d, want 2/1:\n%s",
			executed, refused, out.String())
	}
	if !strings.Contains(refusal, "attempted 3 times") {
		t.Fatalf("refusal %q does not name the loop", refusal)
	}
	data, err := os.ReadFile(filepath.Join(workDir, "out.txt"))
	if err != nil {
		t.Fatalf("write_file output missing: %v", err)
	}
	if string(data) != "hello e2e\n" {
		t.Fatalf("out.txt = %q", data)
	}
}
