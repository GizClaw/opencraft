package host

import (
	"context"
	"errors"
	"testing"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// TestAcquireRefusesUnnamedTarget pins the guard the pool gained with
// Target: an empty workspace path cleans to "." and would otherwise
// assemble (and pool) a runtime for whatever directory the process
// happened to be in. Asking for a Host means naming a target.
func TestAcquireRefusesUnnamedTarget(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	for _, tc := range []struct {
		name   string
		target Target
	}{
		{"zero", Target{}},
		{"blank workspace", WorkspaceTarget("   ")},
		{"blank app", AppTarget("  ")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := m.Acquire(
				context.Background(), tc.target, interact.Auto{}, nil,
			); !errors.Is(err, ErrNoTarget) {
				t.Fatalf("Acquire = %v, want ErrNoTarget", err)
			}
			if _, err := m.Ensure(context.Background(), tc.target); !errors.Is(err, ErrNoTarget) {
				t.Fatalf("Ensure = %v, want ErrNoTarget", err)
			}
			if got := m.Current(tc.target); got != nil {
				t.Fatalf("Current = %p, want no Host for an unnamed target", got)
			}
			if m.pendingAssemblies() != 0 {
				t.Fatal("a refused acquire left an in-flight assembly behind")
			}
		})
	}
}

// TestAppTargetIsNotAssembledAsWorkspace pins the namespace: an
// application target has no assembly yet, and the pool must refuse it
// rather than fall back to the workspace builder and hand out a runtime
// for a directory that was never named. It will assemble an app host
// once capabilities/apps lands; what must never come back is a
// workspace's runtime.
func TestAppTargetIsNotAssembledAsWorkspace(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	if _, err := m.Ensure(
		context.Background(), AppTarget("demo"),
	); err == nil {
		t.Fatal("Ensure assembled an application target")
	}
	if m.pendingAssemblies() != 0 {
		t.Fatal("a refused app assembly left an in-flight entry behind")
	}
}

// TestInvalidationScopesStayInTheirNamespace pins the split of the one
// old InvalidateAll: a workspace reload must leave every application
// alone, and an application's enable/update/uninstall must leave every
// workspace alone. Both scopes share one pool, so the only thing
// keeping them apart is the key.
func TestInvalidationScopesStayInTheirNamespace(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	closed := make(chan *Host, 4)
	recordingClose(m, closed)

	workspace := pooledFakeHost(m, WorkspaceTarget("/workspace/a"), 0)
	app := pooledFakeHost(m, AppTarget("demo"), 0)

	// A workspace reload: the workspace's runtime is retired, the
	// application's is not even looked at.
	m.InvalidateWorkspaces(context.Background())
	if got := m.Current(WorkspaceTarget("/workspace/a")); got != nil {
		t.Fatalf("workspace host %p survived InvalidateWorkspaces", got)
	}
	if got := m.Current(AppTarget("demo")); got != app {
		t.Fatalf("app host after InvalidateWorkspaces = %p, want %p", got, app)
	}

	// And the other way round, for one named application: the
	// application retires, the workspace (re-pooled here) does not.
	m.hosts[workspace.target.Key()] = &hostRef{
		host: workspace, target: workspace.target, refs: 1,
	}
	m.InvalidateApps(context.Background(), "demo")
	if got := m.Current(AppTarget("demo")); got != nil {
		t.Fatalf("app host %p survived InvalidateApps", got)
	}
	if got := m.Current(WorkspaceTarget("/workspace/a")); got != workspace {
		t.Fatalf("workspace host after InvalidateApps = %p, want %p", got, workspace)
	}

	// An unnamed InvalidateApps means every application, never a
	// workspace: that is what "reload the applications" has to mean
	// once a plugin or a settings save is not the trigger.
	m.hosts[app.target.Key()] = &hostRef{host: app, target: app.target, refs: 1}
	m.InvalidateApps(context.Background())
	if got := m.Current(AppTarget("demo")); got != nil {
		t.Fatalf("app host %p survived a bare InvalidateApps", got)
	}
	if got := m.Current(WorkspaceTarget("/workspace/a")); got != workspace {
		t.Fatalf("workspace host after a bare InvalidateApps = %p, want %p",
			got, workspace)
	}
}
