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
	defer rt.Close()
}
