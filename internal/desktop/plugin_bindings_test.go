package desktop

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	session "github.com/GizClaw/flowcraft/core/runtime/session"
	"github.com/GizClaw/opencraft/internal/plugins"
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

func TestPluginToolsReturnsManifestTools(t *testing.T) {
	src := filepath.Join(t.TempDir(), "tool-plugin")
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"id": "tool-plugin",
		"name": "Tool Plugin",
		"version": "1.0.0",
		"entry": "dist/index.js",
		"permissions": ["tools:expose"],
		"capability": {"binary": "bin/cap", "protocol": 1},
		"tools": [
			{"name": "do_thing", "description": "Does a thing", "method": "thing.run", "mutatesState": false},
			{"name": "mutate_thing", "method": "thing.mutate"}
		]
	}`
	if err := os.WriteFile(filepath.Join(src, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "cap"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := plugins.NewStore(t.TempDir())
	if _, err := store.Install(src); err != nil {
		t.Fatalf("install: %v", err)
	}
	a := &App{plugins: store}

	tools, err := a.PluginTools("tool-plugin")
	if err != nil {
		t.Fatalf("PluginTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %+v, want 2", tools)
	}
	if tools[0].Name != "do_thing" || tools[0].Method != "thing.run" ||
		tools[0].MutatesState || tools[0].Description != "Does a thing" {
		t.Fatalf("tools[0] = %+v", tools[0])
	}
	if !tools[1].MutatesState {
		t.Fatalf("tools[1] should default to mutating: %+v", tools[1])
	}
	if _, err := a.PluginTools("missing"); err == nil {
		t.Fatal("PluginTools(missing) must fail")
	}
}
