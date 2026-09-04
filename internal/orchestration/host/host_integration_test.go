package host_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

func writeFakeConfig(t *testing.T, configDir, baseURL string) {
	t.Helper()
	seed := []byte("version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(filepath.Join(configDir, "opencraft.yaml"), seed, 0o600); err != nil {
		t.Fatal(err)
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
	if err := config.WriteInference(configDir, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestHostRunWritesFileEndToEnd(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "write_file",
			Arguments: `{"file_path":"out.txt","content":"host e2e\n"}`,
		}}},
		fakeprovider.Reply{Text: "done"},
	)
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "write out.txt"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	res, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("wait run: %v", err)
	}
	if res == nil || res.Status != "completed" {
		t.Fatalf("result = %+v, want completed", res)
	}
	if provider.Calls() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.Calls())
	}
	data, err := os.ReadFile(filepath.Join(workDir, "out.txt"))
	if err != nil {
		t.Fatalf("write_file output missing: %v", err)
	}
	if string(data) != "host e2e\n" {
		t.Fatalf("out.txt = %q", data)
	}
	if h.Sessions() == nil {
		t.Fatal("host sessions store missing")
	}
	if _, err := os.Stat(filepath.Join(workDir, ".opencraft")); !os.IsNotExist(err) {
		t.Fatalf("project .opencraft must not be created: %v", err)
	}
}

func TestHostRunFiresExternalLifecycleHooks(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	hookDir := t.TempDir()
	startFile := filepath.Join(hookDir, "start.json")
	endFile := filepath.Join(hookDir, "end.json")
	sessionFile := filepath.Join(hookDir, "session.json")
	hookJSON := fmt.Sprintf(`{
		"hooks": {
			"UserPromptSubmit": [{"hooks": [{"command": "cat > %s"}]}],
			"TurnEnd":          [{"hooks": [{"command": "cat > %s"}]}],
			"SessionStart":     [{"hooks": [{"command": "cat > %s"}]}]
		}
	}`, shellQuote(startFile), shellQuote(endFile), shellQuote(sessionFile))
	if err := os.WriteFile(
		filepath.Join(configDir, "hooks.json"), []byte(hookJSON), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hello hooks"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	assertHookPayload(t, startFile, "UserPromptSubmit", map[string]string{
		"conversation_id": run.ContextID(),
		"prompt":          "hello hooks",
	})

	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("wait run: %v", err)
	}
	assertHookPayload(t, endFile, "TurnEnd", map[string]string{
		"conversation_id": run.ContextID(),
		"run_id":          run.RunID(),
		"status":          "completed",
	})

	h.FireHook(ctx, "SessionStart", map[string]any{
		"conversation_id": run.ContextID(),
		"source":          "resume",
	})
	assertHookPayload(t, sessionFile, "SessionStart", map[string]string{
		"conversation_id": run.ContextID(),
		"source":          "resume",
	})
}

func assertHookPayload(
	t *testing.T,
	path, wantEvent string,
	wantFields map[string]string,
) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("hook output %s: %v", path, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode hook output %s: %v", path, err)
	}
	if got, _ := payload["event"].(string); got != wantEvent {
		t.Fatalf("hook event = %q, want %q (payload %s)", got, wantEvent, data)
	}
	for key, want := range wantFields {
		got, _ := payload[key].(string)
		if got != want {
			t.Fatalf("hook %s field = %q, want %q (payload %s)", key, got, want, data)
		}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
