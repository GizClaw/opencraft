package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/delegation/kanban"
	"github.com/GizClaw/flowcraft/core/event"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
)

// TestRuntimeFiresSubagentHooks drives the deployed hook wiring end to
// end: the document's hooks.Runner loads the user's hooks.json, the
// hooks.SubagentObserver resource resolves its declared runner
// dependency, and the observer's subscription is wired to the
// runtime's own bus — so a delegation card transition reaches a
// command hook.
//
// The observer's mapping is covered in flowcraft; what only this
// assembly can pin is the opencraft half: both kinds resolve from the
// embedded document, the dependency is named runner, and Wire runs on
// the assembled runtime (a factory that subscribed in New would be
// invisible here).
func TestRuntimeFiresSubagentHooks(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models:    []config.Model{{Name: "test-model"}},
		}},
	}
	if err := configseed.Write(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}

	// The user's hooks.json: one file per subagent event, so a hook that
	// fires is readable back without racing another event's writer.
	dir := t.TempDir()
	startOut := filepath.Join(dir, "subagent-start.out")
	stopOut := filepath.Join(dir, "subagent-stop.out")
	hooksDoc := fmt.Sprintf(`{
		"hooks": {
			"SubagentStart": [{"hooks": [{"command": "cat >> %s"}]}],
			"SubagentStop":  [{"hooks": [{"command": "cat >> %s"}]}]
		}
	}`, startOut, stopOut)
	if err := os.WriteFile(
		filepath.Join(userDir, "hooks.json"), []byte(hooksDoc), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	mgr, err := config.Open(config.Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	layout := testWorkspaceLayout(t, home, work)
	sessionStore := migratedSessionStore(t, layout)
	ctrl, err := BuildAssembly(
		context.Background(),
		view,
		work,
		WithAutomationHost(automationStub{}),
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithWorkspaceLayout(layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return sessionStore, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildAssembly: %v", err)
	}
	rt := ctrl.Runtime()
	if rt == nil {
		t.Fatal("assembled runtime missing")
	}
	defer func() { _ = ctrl.Close() }()

	// A second board publishing onto the runtime's own bus: the runtime
	// keeps its cards on its own board, so this one cannot be claimed
	// out from under the test.
	busValue, ok := rt.Resource("events")
	if !ok {
		t.Fatal("events resource missing")
	}
	bus, ok := busValue.(event.Bus)
	if !ok || bus == nil {
		t.Fatalf("events resource is %T, want event.Bus", busValue)
	}
	board, err := kanban.New("engine-hooks-test", kanban.WithBus(bus))
	if err != nil {
		t.Fatalf("kanban board: %v", err)
	}
	defer func() { _ = board.Close() }()

	ctx := context.Background()
	id, err := board.Submit(ctx, sdkdelegation.AsyncRequest{
		Request: sdkdelegation.Request{
			Mode:   sdkdelegation.ModeAsync,
			Target: "researcher",
			Input:  "dig",
		},
		Caller: "planner",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !board.ClaimCard(id, "researcher") {
		t.Fatal("ClaimCard refused")
	}
	start := waitForHookBody(t, startOut)
	for _, want := range []string{
		`"event":"SubagentStart"`,
		`"subagent":"researcher"`,
		`"card_id":"` + id + `"`,
	} {
		if !strings.Contains(start, want) {
			t.Fatalf("SubagentStart payload %s lacks %s", start, want)
		}
	}

	if !board.Cancel(id, "test cleanup") {
		t.Fatal("Cancel refused")
	}
	stop := waitForHookBody(t, stopOut)
	for _, want := range []string{
		`"event":"SubagentStop"`,
		`"subagent":"researcher"`,
	} {
		if !strings.Contains(stop, want) {
			t.Fatalf("SubagentStop payload %s lacks %s", stop, want)
		}
	}
}

// waitForHookBody waits for a hook command to write its stdin body and
// returns it. The hook runs on another goroutine, so the wait is
// bounded rather than instantaneous.
func waitForHookBody(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("hook wrote nothing to %s in time", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
