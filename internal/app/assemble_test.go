package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/config"
)

// TestBuildRuntimeAssemblesNewTools verifies that the embedded deploy
// document (with the new tool sources) assembles into a runtime. The
// user layer disables the remote execd backend so the test binary is
// not self-forked as an execd child.
func TestBuildRuntimeAssemblesNewTools(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Seed the user-facing assets (inference.yaml, graphs, prompts).
	if _, err := config.EnsureUserConfig(); err != nil {
		t.Fatalf("seed user config: %v", err)
	}
	// User layer: disable remote execd so the test binary is not
	// self-forked as an execd child.
	layer := "resources:\n  box:\n    settings:\n      remote: false\n"
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
	if _, err := config.EnsureUserConfig(); err != nil {
		t.Fatalf("seed user config: %v", err)
	}

	layer := "resources:\n  box:\n    settings:\n      remote: false\n" +
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
