package hooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/delegation/kanban"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/resource"
)

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s was never written", path)
}

func TestObserverFiresSubagentHooks(t *testing.T) {
	dir := t.TempDir()
	startOut := filepath.Join(dir, "start.out")
	stopOut := filepath.Join(dir, "stop.out")
	cfg := fmt.Sprintf(`{
		"hooks": {
			"SubagentStart": [{"matcher": "*", "hooks": [{"command": "cat > %s"}]}],
			"SubagentStop":  [{"hooks": [{"command": "cat > %s"}]}]
		}
	}`, startOut, stopOut)
	path := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	bus := event.NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	value, err := (ObserverFactory{}).New(ctx, resource.Input{
		Deps: map[string]any{"events": bus, "hooks": mgr},
	})
	if err != nil {
		t.Fatalf("observer factory: %v", err)
	}
	obs, ok := value.(*Observer)
	if !ok {
		t.Fatalf("factory returned %T, want *Observer", value)
	}
	defer obs.Close()

	card := kanban.CardEvent{
		Version:  kanban.PayloadVersion,
		CardID:   "c1",
		ScopeID:  "s1",
		Status:   kanban.StatusClaimed,
		Consumer: "researcher",
		RunID:    "r1",
	}
	env, err := event.NewEnvelope(ctx,
		"delegation.kanban.card.claimed.c1", card)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, env); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, startOut)
	data, err := os.ReadFile(startOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"subagent":"researcher"`) {
		t.Fatalf("SubagentStart payload = %s", data)
	}

	card.Status = kanban.StatusDone
	env, err = event.NewEnvelope(ctx,
		"delegation.kanban.card.done.c1", card)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, env); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, stopOut)
}
