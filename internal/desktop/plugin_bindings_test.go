package desktop

import (
	"sync"
	"testing"

	session "github.com/GizClaw/flowcraft/core/runtime/session"
)

func TestRefreshAgentPluginsDefersDuringTurn(t *testing.T) {
	a := &App{
		mu:    sync.Mutex{},
		turns: map[string]*session.Turn{"run-1": nil},
	}
	if err := a.refreshAgentPlugins(); err != nil {
		t.Fatalf("refreshAgentPlugins during turn: %v", err)
	}
	a.mu.Lock()
	pending := a.pendingRebuild
	a.mu.Unlock()
	if !pending {
		t.Fatal("plugin rebuild must be deferred while a turn is running")
	}
}

func TestMaybeApplyPendingPluginRebuildWhenIdle(t *testing.T) {
	a := &App{
		mu:             sync.Mutex{},
		turns:          map[string]*session.Turn{},
		pendingRebuild: true,
		bridge:         NewBridge(),
	}
	a.maybeApplyPendingRebuild()
	a.mu.Lock()
	pending := a.pendingRebuild
	a.mu.Unlock()
	if pending {
		t.Fatal("pending plugin rebuild must be consumed when no turn is running")
	}
}
