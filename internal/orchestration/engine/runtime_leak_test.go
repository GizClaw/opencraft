package engine

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"weak"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
)

// TestClosedRuntimeIsCollectable guards the runtime lifecycle. Two heap
// profiles of the packaged app, five minutes and five runtime rebuilds
// apart, showed one skills search index surviving per rebuild (0.93MB
// each before the flat postings change, ~20 of them after 48 rebuilds),
// so a retired runtime was still reachable from somewhere. Close is the
// only teardown the engine offers: once the caller drops its handle, the
// runtime and every resource it owns — the skills service included —
// has to become garbage. When this fails, a global registry, an
// unfinished subscription, or a goroutine started per assembly is
// holding the retired generation.
func TestClosedRuntimeIsCollectable(t *testing.T) {
	for _, probe := range buildAndCloseRuntime(t) {
		awaitCollected(t, probe)
	}
}

// leakProbe reports whether one object survived teardown. The builder
// keeps the weak pointers rather than the objects, so nothing in the
// test's own stack can keep them alive.
type leakProbe struct {
	name           string
	stillReachable func() bool
}

// buildAndCloseRuntime assembles one real runtime, tears it down, and
// returns probes for the objects that must be gone by then. Every
// reference lives in this function's frame, which is gone by the time
// the caller collects.
func buildAndCloseRuntime(t *testing.T) []leakProbe {
	t.Helper()
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models:    []config.Model{{Name: "test-model"}},
		}},
	}
	if err := configseed.Write(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}
	mgr, err := config.Open(config.Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	layout := testWorkspaceLayout(t, home, work)
	sessionStore := migratedSessionStore(t, layout)
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithAutomationHost(automationStub{}),
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithWorkspaceLayout(layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return sessionStore, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	value, ok := rt.Resource("skills")
	if !ok {
		t.Fatal("skills resource missing")
	}
	service, ok := value.(*skills.Service)
	if !ok {
		t.Fatalf("skills resource is %T, want *skills.Service", value)
	}

	runtimeProbe := weak.Make(rt)
	skillsProbe := weak.Make(service)

	if err := rt.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	return []leakProbe{
		{
			name:           "runtime",
			stillReachable: func() bool { return runtimeProbe.Value() != nil },
		},
		{
			name:           "skills service",
			stillReachable: func() bool { return skillsProbe.Value() != nil },
		},
	}
}

// awaitCollected runs GC until the probe reports the object is gone.
// Teardown can finish on a goroutine, so a few rounds are allowed
// before declaring the object retained.
func awaitCollected(t *testing.T, probe leakProbe) {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		runtime.GC()
		if !probe.stillReachable() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf(
		"%s is still reachable after Close: something retains the "+
			"retired runtime generation", probe.name)
}
