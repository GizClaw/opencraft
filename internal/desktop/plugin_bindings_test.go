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

func TestPluginSkillsFallsBackWithoutRuntime(t *testing.T) {
	src := filepath.Join(t.TempDir(), "skill-plugin")
	skillDir := filepath.Join(src, "skills", "lark")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"id": "skill-plugin",
		"name": "Skill Plugin",
		"version": "1.0.0",
		"entry": "dist/index.js",
		"permissions": ["skills:contribute"],
		"skills": ["skills/lark"]
	}`
	if err := os.WriteFile(filepath.Join(src, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := `---
name: lark
description: Lark/Feishu skill
---
# Lark
Body.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skill), 0o600); err != nil {
		t.Fatal(err)
	}
	// A store root under a hidden directory reproduces real installs
	// under ~/.opencraft: the direct scan must not treat that hidden
	// parent as a hidden skill directory.
	store := plugins.NewStore(filepath.Join(t.TempDir(), ".opencraft", "plugins"))
	if _, err := store.Install(src); err != nil {
		t.Fatalf("install: %v", err)
	}
	a := &App{plugins: store}

	skills, err := a.PluginSkills("skill-plugin")
	if err != nil {
		t.Fatalf("PluginSkills: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "lark" {
		t.Fatalf("skills = %+v, want [lark]", skills)
	}
	if skills[0].Description != "Lark/Feishu skill" {
		t.Fatalf("description = %q", skills[0].Description)
	}
	if _, err := a.PluginSkills("missing"); err == nil {
		t.Fatal("PluginSkills(missing) must fail")
	}
}

func TestPluginSkillsSkipsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "evil-plugin")
	skillDir := filepath.Join(pluginDir, "skills", "evil")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"id": "evil-plugin",
		"name": "Evil Plugin",
		"version": "1.0.0",
		"entry": "dist/index.js",
		"permissions": ["skills:contribute"],
		"skills": ["skills/evil"]
	}`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	leak := `---
name: leak
description: must not be reachable through plugin manager
---
Body.
`
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte(leak), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(
		filepath.Join(outside, "SKILL.md"),
		filepath.Join(skillDir, "SKILL.md"),
	); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	a := &App{plugins: plugins.NewStore(root)}

	skills, err := a.PluginSkills("evil-plugin")
	if err != nil {
		t.Fatalf("PluginSkills: %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("skills = %+v, want escaping symlink skipped", skills)
	}
}
