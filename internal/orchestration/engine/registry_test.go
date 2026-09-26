package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/resource"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// unusedSessionStore satisfies the registry builder's SessionStore
// requirement; this test only registers factories, it never builds one.
func unusedSessionStore(
	context.Context, string, int,
) (*ocsessions.Store, error) {
	return nil, errors.New("session store unused in registry test")
}

// TestEmbeddedAssetsResolveAgainstRegistry writes down the deploy-time
// promise as a test: every (kind, impl) the embedded assets name —
// resources, agent engines, agent hooks — must resolve in the registry
// registerResources installs.
//
// It exists because a missing registration is otherwise silent: the
// flowcraft windows sandbox backend went unregistered until W1.4, and
// nothing failed until a deployment document actually asked for
// impl: windows ("deploy: resource %q: no factory for %s/%s" from
// flowcraft's builder). This scan runs on every platform, so it also
// guards the platform-specific backends no CI lane deploys.
func TestEmbeddedAssetsResolveAgainstRegistry(t *testing.T) {
	ctx := context.Background()
	mgr, err := config.Open(config.Options{UserDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		t.Fatalf("load embedded layers: %v", err)
	}
	reg := resource.NewRegistry()
	if err := registerResources(reg, &Options{
		SessionStore: unusedSessionStore,
	}); err != nil {
		t.Fatalf("register resources: %v", err)
	}

	lookup := func(what, kind, impl string) {
		t.Helper()
		if _, ok := reg.Lookup(resource.Kind(kind), impl); !ok {
			t.Errorf("%s: no factory for %s/%s", what, kind, impl)
		}
	}
	// The document is deep-merged from every embedded layer, so this
	// covers opencraft.yaml, tools.yaml, agents.yaml and runtime.yaml
	// without hard-coding their file names.
	if len(view.Document.Resources) == 0 {
		t.Fatal("embedded document declares no resources")
	}
	for name, res := range view.Document.Resources {
		lookup(fmt.Sprintf("resource %q", name), string(res.Kind), res.Impl)
	}
	for name, def := range view.Document.Agents {
		lookup(fmt.Sprintf("agent %q engine", name),
			string(def.Engine.Kind), def.Engine.Impl)
		for _, slot := range []struct {
			name  string
			hooks []agent.Hook
		}{
			{agent.HookSlotPreparer, def.Prepare},
			{agent.HookSlotObserver, def.Observe},
			{agent.HookSlotReferee, def.Referees},
			{agent.HookSlotCommitter, def.Commit},
		} {
			for i, hook := range slot.hooks {
				lookup(fmt.Sprintf("agent %q hook %s[%d]", name, slot.name, i),
					"hook."+slot.name, hook.Type)
			}
		}
	}
}

// TestWindowsSandboxBackendIsRegistered pins the W1.4 gap directly:
// the flowcraft windows backend must resolve even though no embedded
// asset selects it, because a user layer is allowed to.
func TestWindowsSandboxBackendIsRegistered(t *testing.T) {
	reg := resource.NewRegistry()
	if err := registerResources(reg, &Options{
		SessionStore: unusedSessionStore,
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Lookup("sandbox.Runner", "windows"); !ok {
		t.Fatal("sandbox.Runner/windows is not registered")
	}
}
