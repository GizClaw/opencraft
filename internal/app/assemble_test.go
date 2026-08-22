package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"

	"github.com/GizClaw/opencraft/internal/agents"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/setup"
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
	cfg := setup.Config{
		Providers: []setup.KeyedProvider{{
			Provider:  setup.Providers[0],
			KeySource: setup.KeyEnv,
		}},
	}
	if err := cfg.Write(userDir); err != nil {
		t.Fatalf("write setup config: %v", err)
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
		Name:         "researcher",
		Description:  "Reads and summarizes the codebase",
		Instructions: "Explore the repository and summarize its architecture.",
		Tools:        agents.ToolsReadOnly,
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

// TestBuildRuntimeUsesUserGraphOverride verifies the user-override
// path: when a config layer points the agent at its own graph file,
// that file (and any node sources it names) is read from the user
// config dir — a missing override fails loudly instead of silently
// falling back to the embedded default graph.
func TestBuildRuntimeUsesUserGraphOverride(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Full user layer: inference wiring (same shape the wizard emits)
	// plus the box override and a custom graph reference.
	layer := "resources:\n" +
		"  provider.deepseek:\n" +
		"    kind: inference.Provider\n" +
		"    impl: deepseek\n" +
		"    settings:\n" +
		"      id: deepseek\n" +
		"      spec:\n" +
		"        api: responses\n" +
		"      profiles:\n" +
		"        - secrets:\n" +
		"            api_key: ${env:DEEPSEEK_API_KEY}\n" +
		"  infer:\n" +
		"    kind: inference.Assembly\n" +
		"    impl: unified\n" +
		"    deps:\n" +
		"      provider: provider.deepseek\n" +
		"  router:\n" +
		"    kind: inference.Router\n" +
		"    impl: unified\n" +
		"    deps:\n" +
		"      target: infer\n" +
		"    settings:\n" +
		"      generate:\n" +
		"        - tier: default\n" +
		"          targets:\n" +
		"            - model:\n" +
		"                id:\n" +
		"                  provider: deepseek\n" +
		"                  name: deepseek-v4-flash\n" +
		"  box:\n" +
		"    settings:\n" +
		"      remote: false\n" +
		"agents:\n  assistant:\n    engine:\n      settings:\n" +
		"        graph: {file: graphs/custom-assistant.yaml}\n"
	if err := os.WriteFile(
		filepath.Join(userDir, "opencraft.yaml"),
		[]byte(layer), 0o600,
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

	// The referenced graph file does not exist yet: building must fail
	// rather than fall back to the embedded default graph.
	if _, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithWorkBase(work),
		WithConfigBase(userDir),
	); err == nil {
		t.Fatal("BuildRuntime with missing custom graph must fail")
	}

	graphDir := filepath.Join(userDir, "graphs")
	if err := os.MkdirAll(graphDir, 0o700); err != nil {
		t.Fatal(err)
	}
	customGraph := `name: custom-assistant
entry: custom
nodes:
  - id: custom
    type: script
    config:
      runtime: js
      source: {file: graphs/nodes/custom.js}
edges:
  - {from: custom, to: __end__}
`
	if err := os.WriteFile(
		filepath.Join(graphDir, "custom-assistant.yaml"),
		[]byte(customGraph), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(graphDir, "nodes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(graphDir, "nodes", "custom.js"),
		[]byte("// custom graph node\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithWorkBase(work),
		WithConfigBase(userDir),
	)
	if err != nil {
		t.Fatalf("BuildRuntime with custom graph: %v", err)
	}
	defer func() { _ = rt.Close() }()
}
