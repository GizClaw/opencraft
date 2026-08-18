package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
