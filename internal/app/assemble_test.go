package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/agents"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/hooks"
	"github.com/GizClaw/opencraft/internal/plugins"
	pluginagent "github.com/GizClaw/opencraft/internal/plugins/agent"
	"github.com/GizClaw/opencraft/internal/skills"
)

// TestBuildRuntimeAssemblesNewTools verifies that the embedded deploy
// document (with the new tool sources) assembles into a runtime. The
// inference wiring comes from the wizard-generated user layer, and the
// project layer disables the remote execd backend so the test binary
// is not self-forked as an execd child.
func TestBuildRuntimeAssemblesNewTools(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// First-run wizard output: DeepSeek keyed, provider wired into the
	// infer assembly.
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
		}},
	}
	if err := config.WriteInference(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}
	// Project layer: disable remote execd so the test binary is not
	// self-forked as an execd child.
	projectDir := filepath.Join(work, ".opencraft", "config")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, "opencraft.yaml"),
		[]byte("resources:\n  box:\n    settings:\n      remote: false\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	mgr, err := config.Open(config.Options{
		WorkDir: work,
		UserDir: userDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithWorkBase(work),
		WithConfigBase(userDir),
	)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	defer func() { _ = rt.Close() }()

	// Delegation wiring: the service and kanban backend resources
	// build, and the deployed assistant is a delegate target (the
	// deploy builder binds the directory through DeploymentBinder, so
	// a successful build implies the bind succeeded).
	serviceValue, ok := rt.Resource("delegate")
	if !ok {
		t.Fatal("delegate resource missing")
	}
	service, ok := serviceValue.(sdkdelegation.Service)
	if !ok || service == nil {
		t.Fatal("delegate resource is not a delegation.Service")
	}
	backendValue, ok := rt.Resource("delegate.backend")
	if !ok {
		t.Fatal("delegate.backend resource missing")
	}
	backend, ok := backendValue.(sdkdelegation.AsyncBackend)
	if !ok || backend == nil {
		t.Fatal("delegate.backend resource is not a delegation.AsyncBackend")
	}
	if _, ok := rt.Agent("assistant"); !ok {
		t.Fatal("assistant agent not deployed as a delegation target")
	}

	// Persistent subagent wiring: the lifecycle resource builds and is
	// bound to the runtime, and a created agent immediately becomes a
	// delegation target (core v0.1.24 dynamic directory).
	lifecycleValue, ok := rt.Resource("agentlifecycle")
	if !ok {
		t.Fatal("agentlifecycle resource missing")
	}
	lifecycle, ok := lifecycleValue.(*agents.Lifecycle)
	if !ok || lifecycle == nil {
		t.Fatal("agentlifecycle is not a *agents.Lifecycle")
	}
	if _, err := lifecycle.Create(context.Background(), agents.AgentSpec{
		Name:        "researcher",
		Description: "Reads and summarizes the codebase",
		Graph:       `{"name":"researcher","entry":"llm","nodes":[{"id":"llm","type":"inference","config":{"system_prompt":"Read-only researcher."}}],"edges":[{"from":"llm","to":"__end__"}]}`,
	}); err != nil {
		t.Fatalf("Create persistent agent: %v", err)
	}
	dirValue, ok := rt.Resource("delegate.directory")
	if !ok {
		t.Fatal("delegate.directory resource missing")
	}
	directory, ok := dirValue.(*sdkdelegation.LocalDirectory)
	if !ok || directory == nil {
		t.Fatal("delegate.directory is not a *delegation.LocalDirectory")
	}
	targets, err := directory.List(context.Background())
	if err != nil {
		t.Fatalf("directory.List: %v", err)
	}
	found := false
	for _, target := range targets {
		if target.ID == "researcher" {
			found = true
		}
	}
	if !found {
		t.Fatalf("created agent missing from delegation targets: %+v", targets)
	}
}

func TestBuildRuntimeWithPluginHostExposesAgentCapabilities(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
		}},
	}
	if err := config.WriteInference(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}
	projectDir := filepath.Join(work, ".opencraft", "config")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, "opencraft.yaml"),
		[]byte("resources:\n  box:\n    settings:\n      remote: false\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	// A real plugin on disk: one skill, one hook file, one capability
	// tool declaration.
	pluginRoot := t.TempDir()
	plugDir := filepath.Join(pluginRoot, "plug")
	for _, dir := range []string{
		filepath.Join(plugDir, "dist"),
		filepath.Join(plugDir, "skills", "hello"),
		filepath.Join(plugDir, "hooks"),
		filepath.Join(plugDir, "bin"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := map[string]any{
		"id": "plug", "name": "Plug", "version": "0.1.0",
		"entry":      "dist/index.js",
		"capability": map[string]any{"binary": "bin/ping", "protocol": 1},
		"permissions": []string{
			"skills:contribute", "hooks:register", "tools:expose",
		},
		"skills": []string{"skills"},
		"hooks":  []string{"hooks/hooks.json"},
		"tools": []any{map[string]any{
			"name": "ping", "description": "Ping", "method": "ping",
		}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"plugin.json":   string(raw),
		"dist/index.js": "export const apply = () => {};",
		"skills/hello/SKILL.md": "---\nname: hello\n" +
			"description: Hello plugin skill\n---\nBody.\n",
		"hooks/hooks.json": `{
			"hooks": {
				"PreToolUse": [{"hooks": [{"command": "cat > plugin-hook.out"}]}]
			}
		}`,
		"bin/ping": "#!/bin/sh\nexit 0\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(plugDir, rel), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	host := pluginagent.NewHost(plugins.NewStore(pluginRoot), nil)
	if roots := host.SkillRoots(); len(roots) != 1 {
		t.Fatalf("plugin host skill roots = %v, want the plugin skills dir", roots)
	}

	mgr, err := config.Open(config.Options{
		WorkDir: work,
		UserDir: userDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithAgentPlugins(host),
	)
	if err != nil {
		t.Fatalf("BuildRuntime with plugin host: %v", err)
	}
	defer func() { _ = rt.Close() }()

	// Plugin skill is part of the shared skills registry.
	skillsValue, ok := rt.Resource("skills")
	if !ok {
		t.Fatal("skills resource missing")
	}
	svc, ok := skillsValue.(*skills.Service)
	if !ok || svc == nil {
		t.Fatal("skills resource is not *skills.Service")
	}
	foundSkill := false
	for _, sk := range svc.List() {
		if sk.Name == "hello" {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Fatalf("plugin skill not discovered: roots=%v scan=%v errors=%v",
			svc.Roots(), svc.Errors(), svc.List())
	}

	// Plugin hook is merged into the runtime hooks manager and runs
	// with the plugin directory as cwd.
	hooksValue, ok := rt.Resource("hooks")
	if !ok {
		t.Fatal("hooks resource missing")
	}
	hookMgr, ok := hooksValue.(*hooks.Manager)
	if !ok || hookMgr == nil || hookMgr.Empty() {
		t.Fatal("hooks resource is not a non-empty *hooks.Manager")
	}
	hookMgr.Fire(context.Background(), hooks.EventPreToolUse, map[string]any{
		"event": hooks.EventPreToolUse,
		"tool":  "exec_command",
	})
	if _, err := os.Stat(filepath.Join(plugDir, "plugin-hook.out")); err != nil {
		t.Fatalf("plugin hook did not run through the runtime: %v", err)
	}

	// Plugin capability tool is in the assembled tool catalog.
	toolsValue, ok := rt.Resource("tools")
	if !ok {
		t.Fatal("tools resource missing")
	}
	asm, ok := toolsValue.(*tool.Assembly)
	if !ok || asm == nil {
		t.Fatal("tools resource is not *tool.Assembly")
	}
	foundTool := false
	for _, def := range asm.Catalog().Definitions() {
		if def.Name == "plug:ping" {
			foundTool = true
			break
		}
	}
	if !foundTool {
		t.Fatal("plugin capability tool missing from tool catalog")
	}
}
