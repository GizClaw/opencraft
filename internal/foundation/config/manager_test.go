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
	// The user layer is wizard-owned and overlays the embedded layers.
	writeConfig(t, userDir, "opencraft.yaml",
		"resources:\n  infer:\n    kind: inference.Assembly\n    impl: unified\n")

	mgr, err := Open(Options{UserDir: userDir})
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
}

func TestLoadWithoutUserLayer(t *testing.T) {
	// Before the settings page writes a user layer, Load must succeed
	// with the embedded layers alone. Inference wiring is generated
	// into the user layer, so the merged document carries no infer
	// assembly and no router yet: that absence is what the desktop
	// reads as "not configured" and routes to the setup page.
	userDir := t.TempDir()
	mgr, err := Open(Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := view.Document.Resources["infer"]; ok {
		t.Fatal("infer must not exist before the user layer declares it")
	}
	if _, ok := view.Document.Resources["router"]; ok {
		t.Fatal("router must not exist before the user layer declares it")
	}
	configured, err := RouterConfigured(view.Document)
	if err != nil {
		t.Fatal(err)
	}
	if configured {
		t.Fatal("a deployment without inference wiring must read as unconfigured")
	}
}
