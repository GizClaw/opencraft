package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/e2e/fakeprovider"
	"github.com/GizClaw/opencraft/internal/headless"
	"github.com/GizClaw/opencraft/internal/rollout"
)

func writeFakeInferenceConfig(t *testing.T, dir, baseURL string) {
	t.Helper()
	// Pre-seed the user layer with local (non-execd) sandboxing: the
	// test binary cannot fork itself into execd mode (that branch lives
	// in the real main package), so headless E2E uses the in-process
	// platform backend. WriteInference below preserves this key.
	seed := []byte("version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(filepath.Join(dir, "opencraft.yaml"), seed, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      "openai",
			Name:      "fake",
			API:       "chat",
			Endpoint:  baseURL,
			Enabled:   true,
			KeySource: config.KeyLiteral,
			KeyValue:  "test-key",
			Models:    []config.Model{{Name: "fake-model"}},
		}},
	}
	if err := config.WriteInference(dir, cfg); err != nil {
		t.Fatalf("WriteInference: %v", err)
	}
}

func TestHeadlessRunWritesFileViaToolCall(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "write_file",
			Arguments: `{"file_path":"out.txt","content":"hello e2e\n"}`,
		}}},
		fakeprovider.Reply{Text: "done"},
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
	if provider.Calls() != 2 {
		t.Fatalf("provider calls = %d, want 2 (tool call + completion)", provider.Calls())
	}
	data, err := os.ReadFile(filepath.Join(workDir, "out.txt"))
	if err != nil {
		t.Fatalf("write_file output missing: %v", err)
	}
	if string(data) != "hello e2e\n" {
		t.Fatalf("out.txt = %q", data)
	}

	// JSONL contract: tool call, tool result, assistant message, and
	// terminal turn event must all be present.
	var sawToolCall, sawToolResult, sawMessage, sawCompleted bool
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var ev rollout.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid JSONL event %q: %v", line, err)
		}
		switch ev.Type {
		case rollout.TypeItemToolCall:
			sawToolCall = ev.Tool == "write_file"
		case rollout.TypeItemToolResult:
			sawToolResult = true
		case rollout.TypeItemAssistantMsg:
			sawMessage = strings.Contains(ev.Content, "done")
		case rollout.TypeTurnCompleted:
			sawCompleted = ev.Status == "completed"
		}
	}
	for name, ok := range map[string]bool{
		"tool_call": sawToolCall, "tool_result": sawToolResult,
		"assistant_message": sawMessage, "turn.completed": sawCompleted,
	} {
		if !ok {
			t.Fatalf("missing JSONL event %s:\n%s", name, out.String())
		}
	}
}

func TestHeadlessRunProviderFailureFailsClosed(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	provider.Close() // force connection errors
	workDir := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeInferenceConfig(t, configDir, provider.URL())

	res, err := headless.Run(context.Background(), headless.Options{
		WorkDir:   workDir,
		ConfigDir: configDir,
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("headless.Run returned error, want a failed result: %v", err)
	}
	if res.ExitCode == 0 || res.Status == "completed" {
		t.Fatalf("result = %+v, want failure", res)
	}
}
