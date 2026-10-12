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

// TestViewCarriesTheLayerSplit pins the two halves a craft-based
// assembly names separately: the embedded base (the definition, fixed
// for this binary) and the on-disk overlay (what a reload re-reads).
// The overlay is the layers in merge order, explicit below user, and
// the document is the merge of both halves.
func TestViewCarriesTheLayerSplit(t *testing.T) {
	userDir := t.TempDir()
	writeConfig(t, userDir, "opencraft.yaml", "version: v1\n")
	explicitDir := t.TempDir()
	writeConfig(t, explicitDir, "explicit.yaml", "version: v1\n")

	mgr, err := Open(Options{
		UserDir:  userDir,
		Explicit: filepath.Join(explicitDir, "explicit.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Base) != 4 {
		t.Fatalf("base layers = %d, want the four embedded assets", len(view.Base))
	}
	for i, want := range []string{
		"embedded", "embedded-tools", "embedded-agents", "embedded-runtime",
	} {
		if view.Base[i].Name != want {
			t.Errorf("base[%d] = %q, want %q", i, view.Base[i].Name, want)
		}
	}
	if len(view.Overlay) != 2 {
		t.Fatalf("overlay layers = %d, want the explicit and user layers",
			len(view.Overlay))
	}
	if view.Overlay[0].Name != "explicit" || view.Overlay[0].Priority != 5 {
		t.Errorf("overlay[0] = %+v, want the explicit layer at priority 5",
			view.Overlay[0])
	}
	if view.Overlay[1].Name != string(LayerUser) || view.Overlay[1].Priority != 10 {
		t.Errorf("overlay[1] = %+v, want the user layer at priority 10",
			view.Overlay[1])
	}
	if view.Document.Version == "" {
		t.Fatalf("merged document carries no version: %+v", view.Document)
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
