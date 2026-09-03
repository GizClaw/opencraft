package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLayeredDocuments(t *testing.T) {
	userDir := t.TempDir()
	workDir := t.TempDir()
	// The user layer is wizard-owned: its infer resource replaces the
	// (removed) embedded default and must show up with user provenance.
	writeConfig(t, userDir, "opencraft.yaml",
		"resources:\n  infer:\n    kind: inference.Assembly\n    impl: unified\n")

	mgr, err := Open(Options{WorkDir: workDir, UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Document.Resources["infer"].Kind != "inference.Assembly" {
		t.Fatalf("merged infer resource = %+v", view.Document.Resources["infer"])
	}
	if view.Provenance.Resources["infer"].Name != "user" {
		t.Fatalf("infer provenance = %+v, want user", view.Provenance.Resources["infer"])
	}
	if view.Provenance.Resources["events"].Name != "embedded" {
		t.Fatalf("provenance = %+v", view.Provenance.Resources["events"])
	}
}

func TestLoadWithoutUserLayer(t *testing.T) {
	// Before the settings page writes a user layer, Load must succeed
	// with the embedded layers alone. The fixed inference wiring
	// (providers + infer + router retry shell) is embedded, but the
	// router has no generate targets until the user layer declares one.
	userDir := t.TempDir()
	workDir := t.TempDir()
	mgr, err := Open(Options{WorkDir: workDir, UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Document.Resources["infer"].Kind; got != "inference.Assembly" {
		t.Fatalf("infer = %q, want embedded inference.Assembly", got)
	}
	if view.Provenance.Resources["infer"].Name != "embedded-inference" {
		t.Fatalf("infer provenance = %+v", view.Provenance.Resources["infer"])
	}
	if _, ok := view.Document.Resources["router"]; !ok {
		t.Fatal("router shell must exist from embedded inference layer")
	}
}

func TestLoadWorkspaceLayerAboveUser(t *testing.T) {
	userDir := t.TempDir()
	workspaceDir := t.TempDir()
	workspaceConfig := filepath.Join(workspaceDir, "opencraft.yaml")
	writeConfig(t, userDir, "opencraft.yaml",
		"resources:\n  box:\n    impl: opencraft\n")
	writeConfig(t, workspaceDir, "opencraft.yaml",
		"resources:\n  box:\n    impl: local\n")

	mgr, err := Open(Options{
		WorkDir:         t.TempDir(),
		UserDir:         userDir,
		WorkspaceConfig: workspaceConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Document.Resources["box"].Impl; got != "local" {
		t.Fatalf("box impl = %q, want workspace override local", got)
	}
	if view.Provenance.Resources["box"].Name != string(LayerWorkspace) {
		t.Fatalf("provenance = %+v, want workspace", view.Provenance.Resources["box"])
	}
	if mgr.WorkspaceConfig() != workspaceConfig {
		t.Fatalf("WorkspaceConfig = %q", mgr.WorkspaceConfig())
	}
}

func TestLoadWithoutWorkspaceConfigSkipsWorkspaceLayer(t *testing.T) {
	userDir := t.TempDir()
	mgr, err := Open(Options{
		WorkDir: t.TempDir(),
		UserDir: userDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Provenance.Resources["box"].Name == string(LayerWorkspace) {
		t.Fatal("workspace layer unexpectedly applied")
	}
	if mgr.WorkspaceConfig() != "" {
		t.Fatalf("WorkspaceConfig = %q, want empty", mgr.WorkspaceConfig())
	}
}

func TestManagerAcceptsTypedWorkspaceLayout(t *testing.T) {
	dataDir := t.TempDir()
	workDir := t.TempDir()
	layout, err := ResolveWorkspace(dataDir, workDir)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, layout.Root, "opencraft.yaml",
		"resources:\n  box:\n    impl: local\n")
	userDir := t.TempDir()
	mgr, err := Open(Options{
		WorkDir:         workDir,
		UserDir:         userDir,
		WorkspaceLayout: &layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if mgr.WorkspaceConfig() != layout.ConfigFile {
		t.Fatalf("workspace config = %q, want %q",
			mgr.WorkspaceConfig(), layout.ConfigFile)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Document.Resources["box"].Impl; got != "local" {
		t.Fatalf("box impl = %q, want workspace local", got)
	}
}
