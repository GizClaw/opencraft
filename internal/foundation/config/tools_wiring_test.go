package config

import (
	"context"
	"testing"
)

// TestEmbeddedToolsWireViewImage pins the deploy wiring: the tool has
// to be both declared as a source and listed in the tools assembly, or
// the model never sees it.
func TestEmbeddedToolsWireViewImage(t *testing.T) {
	userDir := t.TempDir()
	mgr, err := Open(Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source, ok := view.Document.Resources["tool.viewimage"]
	if !ok {
		t.Fatal("tool.viewimage source is not declared")
	}
	if source.Impl != "opencraft/viewimage" {
		t.Fatalf("tool.viewimage impl = %q", source.Impl)
	}
	if source.Deps["hostworkspace"] != "hostws" {
		t.Fatalf("tool.viewimage deps = %+v", source.Deps)
	}
	tools, ok := view.Document.Resources["tools"]
	if !ok {
		t.Fatal("tools assembly is not declared")
	}
	if tools.Deps["tool.viewimage"] != "tool.viewimage" {
		t.Fatalf("tools assembly does not include tool.viewimage: %+v", tools.Deps)
	}
}
