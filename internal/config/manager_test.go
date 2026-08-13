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
	writeConfig(t, userDir, "opencraft.yaml", "# user layer (empty for now)\n")

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
	if view.Provenance.Resources["events"].Name != "embedded" {
		t.Fatalf("provenance = %+v", view.Provenance.Resources["events"])
	}
}

func TestLoadProjectLayerOverridesSandbox(t *testing.T) {
	userDir := t.TempDir()
	workDir := t.TempDir()
	writeConfig(t, userDir, "opencraft.yaml", "# user layer (empty for now)\n")
	writeConfig(t, filepath.Join(workDir, ".opencraft", "config"), "opencraft.yaml",
		"resources:\n  box:\n    impl: local\n")

	mgr, err := Open(Options{WorkDir: workDir, UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Document.Resources["box"].Impl; got != "local" {
		t.Fatalf("box impl = %q, want project override local", got)
	}
	if view.Provenance.Resources["box"].Name != "project" {
		t.Fatalf("provenance = %+v", view.Provenance.Resources["box"])
	}
	if mgr.ProjectDir() == "" {
		t.Fatal("project dir not discovered")
	}
}
