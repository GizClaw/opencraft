package host_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
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
	if err := configseed.Write(configDir, cfg); err != nil {
		t.Fatal(err)
	}
}

// acquireHostFixture builds the standard Host test fixture: a fake
// provider configured under a throwaway data dir, a manager, and one
// acquired Host that the test closes with itself. It returns the host
// and the workspace directory the runs work in.
func acquireHostFixture(
	t *testing.T, provider *fakeprovider.Server,
) (*host.Host, string) {
	t.Helper()
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	h, err := mgr.Acquire(context.Background(), workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, workDir
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
	metas, err := h.Sessions().List()
	if err != nil {
		t.Fatalf("list sessions after start: %v", err)
	}
	var seeded bool
	for _, meta := range metas {
		if meta.ID != run.ContextID() {
			continue
		}
		if meta.Title != "write out.txt" || meta.Turns != 0 {
			t.Fatalf("seeded session meta = %+v, want titled zero-turn row", meta)
		}
		seeded = true
	}
	if !seeded {
		t.Fatalf("session %s missing from list after start", run.ContextID())
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

// TestHostTapsSandboxProcessOutput is the end-to-end contract of the
// conversation-scoped process feed: a command the model runs through
// exec_command lands in Host.Processes with its output tail, without
// the model (or anyone) reading the session first.
func TestHostTapsSandboxProcessOutput(t *testing.T) {
	if goruntime.GOOS == "windows" {
		// The fixture is POSIX-shell shaped; the feed itself is
		// platform-independent and the sandbox package covers the
		// tap semantics on every backend.
		t.Skip("POSIX shell fixture")
	}
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "exec_command",
			Arguments: `{"command":"echo host-tap-ok"}`,
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

	// YOLO keeps the test off the approval prompt; both chains route
	// through the same tap.
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "run echo"),
		Mode:          ocsessions.ModeYOLO,
		SkipAutoTitle: true,
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

	procs := h.Processes(run.ContextID())
	if len(procs) == 0 {
		t.Fatal("process feed is empty after exec_command")
	}
	var tapped bool
	for _, proc := range procs {
		if !strings.Contains(strings.Join(proc.Argv, " "), "host-tap-ok") {
			continue
		}
		tapped = true
		if !strings.Contains(proc.Tail, "host-tap-ok") {
			t.Errorf("process tail = %q, want the command's output", proc.Tail)
		}
		if proc.Running {
			t.Error("finished command still reported as running")
		}
		if proc.ExitCode == nil || *proc.ExitCode != 0 {
			t.Errorf("exit code = %v, want 0", proc.ExitCode)
		}
	}
	if !tapped {
		t.Fatalf("tapped processes = %+v, want the exec_command session", procs)
	}
	// Other conversations do not see this workspace's processes.
	if other := h.Processes("someone-else"); len(other) != 0 {
		t.Errorf("unrelated conversation sees %d processes, want 0", len(other))
	}
}

// TestHostConcurrentRunsArchiveMatchesStream verifies the real host can
// run several conversations on the same workspace at once, that each
// UI sink sees its own stream text, and that TurnByRunID returns
// exactly that archived turn after turn_end. This is the backend half
// of the turn-end reconciliation contract.
func TestHostConcurrentRunsArchiveMatchesStream(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "final archive text"})
	prompts := []string{
		"alpha", "beta", "gamma", "delta", "epsilon", "zeta",
	}
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

	var mu sync.Mutex
	streamText := make(map[string]string)
	sink := agent.StreamSinkFunc(func(
		_ context.Context,
		env event.Envelope,
		delta agent.StreamDeltaPayload,
	) error {
		if !agent.IsStreamDelta(env.Subject) {
			return nil
		}
		parts := strings.Split(string(env.Subject), ".")
		if len(parts) < 3 || parts[1] != "run" {
			return nil
		}
		runID := parts[2]
		if delta.Type != agent.StreamDeltaPart {
			return nil
		}
		if tp, ok := delta.Part.(message.TextPart); ok && tp.Text != "" {
			mu.Lock()
			streamText[runID] += tp.Text
			mu.Unlock()
		}
		return nil
	})

	runs := make([]*host.Run, 0, len(prompts))
	for _, prompt := range prompts {
		run, err := h.StartRun(ctx, host.RunOptions{
			Message:   message.NewTextMessage(message.RoleUser, prompt),
			Sink:      sink,
			QueueSize: 64,
		})
		if err != nil {
			t.Fatalf("start run %q: %v", prompt, err)
		}
		runs = append(runs, run)
	}

	for _, run := range runs {
		res, err := run.Wait(ctx)
		if err != nil {
			t.Fatalf("wait run %s: %v", run.RunID(), err)
		}
		if res == nil || res.Status != "completed" {
			t.Fatalf("run %s result = %+v, want completed", run.RunID(), res)
		}
	}

	for i, run := range runs {
		prompt := prompts[i]
		mu.Lock()
		gotStream := streamText[run.RunID()]
		mu.Unlock()
		if gotStream != "final archive text" {
			t.Fatalf("run %s stream text = %q, want final archive text",
				run.RunID(), gotStream)
		}
		turn, err := h.Sessions().TurnByRunID(
			ctx, run.ContextID(), run.RunID(),
		)
		if err != nil {
			t.Fatalf("TurnByRunID(%s): %v", run.RunID(), err)
		}
		if turn.Status != "completed" {
			t.Fatalf("turn %s status = %q", run.RunID(), turn.Status)
		}
		userText := ""
		text := ""
		for _, m := range turn.Messages {
			if m.Role == message.RoleUser {
				userText += m.Content.Text()
			}
			if m.Role == message.RoleAssistant {
				text += m.Content.Text()
			}
		}
		if userText != prompt {
			t.Fatalf("turn %s user text = %q, want %q",
				run.RunID(), userText, prompt)
		}
		if text != "final archive text" {
			t.Fatalf("turn %s archive text = %q, want final archive text",
				run.RunID(), text)
		}
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
