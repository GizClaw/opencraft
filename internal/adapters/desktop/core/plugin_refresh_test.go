package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestRefreshPluginRuntimeRebuildsOncePerRevision pins the desktop half
// of the register clock (see capabilities/plugins' charter,
// FaceRefreshes): the runtime is rebuilt only when the registry
// revision moved, a burst of mutations costs one rebuild, and the
// rebuilt runtime actually carries the new plugin.
func TestRefreshPluginRuntimeRebuildsOncePerRevision(t *testing.T) {
	c, workDir, _ := newInstallerTestCore(t)
	ctx := context.Background()
	first, err := c.Runtime.EnsureHost(ctx, workDir)
	if err != nil {
		t.Fatalf("ensure host: %v", err)
	}

	// Nothing moved since the runtime was assembled: no rebuild.
	if err := c.RefreshPluginRuntime(ctx); err != nil {
		t.Fatalf("refresh without a registry change: %v", err)
	}
	if c.ActiveHost() != first {
		t.Fatal("refresh without a registry change rebuilt the runtime")
	}

	// One install moves the revision; one refresh replaces the Host,
	// and the plugin's skill is part of the new assembly.
	src := filepath.Join(workDir, ".opencraft-plugins", "hello")
	writePluginSource(t, src, "hello", "0.1.0")
	if _, err := c.Plugin.Store.Install(src); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := c.RefreshPluginRuntime(ctx); err != nil {
		t.Fatalf("refresh after install: %v", err)
	}
	second := c.ActiveHost()
	if second == nil || second == first {
		t.Fatalf("refresh did not replace the host: %v -> %v", first, second)
	}
	value, ok := second.Controller().Runtime().Resource("skills")
	if !ok {
		t.Fatal("skills resource missing after refresh")
	}
	svc, ok := value.(*skills.Service)
	if !ok || svc == nil {
		t.Fatal("skills resource is not *skills.Service")
	}
	found := false
	for _, sk := range svc.List() {
		if sk.Name == "hello" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("plugin skill not discovered after refresh: roots=%v errors=%v",
			svc.Roots(), svc.Errors())
	}

	// The same revision again: no rebuild.
	if err := c.RefreshPluginRuntime(ctx); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if c.ActiveHost() != second {
		t.Fatal("second refresh at the same revision rebuilt the runtime")
	}

	// Two mutations before one refresh collapse into a single rebuild:
	// the caller of the last mutation covers everything that landed
	// before it, and later callers find the runtime current.
	recorder := logcapture.Install(t)
	secondSrc := filepath.Join(workDir, ".opencraft-plugins", "bye")
	writePluginSource(t, secondSrc, "bye", "0.1.0")
	if _, err := c.Plugin.Store.Install(secondSrc); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if err := c.Plugin.Store.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := c.RefreshPluginRuntime(ctx); err != nil {
		t.Fatalf("refresh after burst: %v", err)
	}
	third := c.ActiveHost()
	if third == nil || third == second {
		t.Fatalf("burst refresh did not replace the host: %v -> %v",
			second, third)
	}
	if got := countInvalidations(recorder); got != 1 {
		t.Fatalf("two mutations before one refresh invalidated the runtime "+
			"%d times, want 1", got)
	}
	if err := c.RefreshPluginRuntime(ctx); err != nil {
		t.Fatalf("refresh after burst settled: %v", err)
	}
	if c.ActiveHost() != third {
		t.Fatal("refresh after a settled burst rebuilt the runtime again")
	}
	if got := countInvalidations(recorder); got != 1 {
		t.Fatalf("a settled burst invalidated the runtime %d times, want 1",
			got)
	}
}
