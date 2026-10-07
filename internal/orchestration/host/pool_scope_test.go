package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// TestAcquireRefusesUnnamedTarget pins the guard the pool gained with
// Target: an empty workspace path cleans to "." and would otherwise
// assemble (and pool) a runtime for whatever directory the process
// happened to be in. Asking for a Host means naming a target, and the
// refusal comes before the assembler runs at all.
func TestAcquireRefusesUnnamedTarget(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	var builds int
	m.assembleHost = func(
		context.Context, Target, interact.Backend, func(string) interact.Backend,
	) (*Host, error) {
		builds++
		return nil, errors.New("assembly ran for an unnamed target")
	}
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
		})
	}
	if builds != 0 {
		t.Fatalf("an unnamed target reached the assembler %d times", builds)
	}
}

// TestAppTargetIsNotAssembledAsWorkspace pins the namespace: an
// application target has no assembly yet, and the pool must refuse it
// rather than fall back to the workspace builder and hand out a runtime
// for a directory that was never named. It will assemble an app host
// once capabilities/apps lands; what must never come back is a
// workspace's runtime.
//
// The refusal itself is what is asserted, not just "some error": a
// workspace assembly in this fixture fails on its own (there is no
// document to build from), so an error-only assertion passes with the
// fallback in place. ErrNoAssembly names the target, which only the
// refusal does.
func TestAppTargetIsNotAssembledAsWorkspace(t *testing.T) {
	recorder := logcapture.Install(t)
	m := NewManagerAt(t.TempDir(), t.TempDir())
	app := AppTarget("demo")

	_, err := m.Ensure(context.Background(), app)
	if !errors.Is(err, ErrNoAssembly) {
		t.Fatalf("Ensure(%s) = %v, want ErrNoAssembly", app, err)
	}
	if got := err.Error(); !strings.Contains(got, "app=demo") {
		t.Fatalf("refusal = %q, want it to name the target", got)
	}
	if got := m.Current(app); got != nil {
		t.Fatalf("a refused app assembly published %p", got)
	}
	if ref := m.hosts[app.Key()]; ref != nil {
		t.Fatalf("a refused app assembly left pool entry %+v", ref)
	}
	if _, err := m.Acquire(
		context.Background(), app, interact.Auto{}, nil,
	); !errors.Is(err, ErrNoAssembly) {
		t.Fatalf("Acquire(%s) = %v, want ErrNoAssembly", app, err)
	}
	// Both refusals are logged, and each line names the target that was
	// refused rather than a work dir the app does not have.
	var failures int
	for _, record := range recorder.Records() {
		if record.Body().AsString() != "host: runtime assembly failed" {
			continue
		}
		failures++
		if got := logcapture.Attribute(record, "target"); got != app.String() {
			t.Fatalf("assembly failure logged target = %q, want %q", got, app)
		}
	}
	if failures != 2 {
		t.Fatalf("assembly-failure lines = %d, want one per refused attempt; bodies: %v",
			failures, recorder.Bodies())
	}
}

// assertPooledHosts pins "these Hosts are still the pool's answer for
// their targets": each pool entry points at its Host, none was marked
// stale, and none was sent to teardown.
func assertPooledHosts(t *testing.T, m *Manager, closed chan *Host, hs ...*Host) {
	t.Helper()
	for _, h := range hs {
		ref := m.hosts[h.target.Key()]
		if ref == nil || ref.host != h {
			t.Fatalf("%s is no longer the pooled host", h.target)
		}
		if h.IsStale() {
			t.Fatalf("%s was marked stale", h.target)
		}
		if got := m.Current(h.target); got != h {
			t.Fatalf("Current(%s) = %p, want %p", h.target, got, h)
		}
	}
	select {
	case got := <-closed:
		t.Fatalf("%s was closed while another scope was invalidated", got.target)
	default:
	}
}

// assertRetiredHosts pins the other half, as a set: exactly these Hosts
// left the pool, each marked stale and each handed to the close path.
// Which order they were invalidated in is the map's business — a
// reload of several targets walks the pool, not a list — so the
// assertion is on membership, not on the sequence.
func assertRetiredHosts(t *testing.T, m *Manager, closed chan *Host, hs ...*Host) {
	t.Helper()
	want := make(map[*Host]bool, len(hs))
	for _, h := range hs {
		want[h] = true
		if ref := m.hosts[h.target.Key()]; ref != nil {
			t.Fatalf("%s stayed in the pool after being invalidated", h.target)
		}
		if !h.IsStale() {
			t.Fatalf("%s was invalidated without being marked stale", h.target)
		}
	}
	got := make(map[*Host]bool, len(want))
	for len(got) < len(want) {
		select {
		case h := <-closed:
			if !want[h] {
				t.Fatalf("unexpected close of %s", h.target)
			}
			got[h] = true
		default:
			t.Fatalf("only %d of %d invalidated hosts were closed",
				len(got), len(want))
		}
	}
	select {
	case h := <-closed:
		t.Fatalf("unexpected close of %s", h.target)
	default:
	}
}

// scopeFixture pools one workspace and two applications in one manager:
// the smallest pool in which "the named one only" and "every application
// but no workspace" are distinguishable from "everything" and from
// "nothing".
type scopeFixture struct {
	m      *Manager
	closed chan *Host
	ws     *Host
	demo   *Host
	other  *Host
}

func newScopeFixture(t *testing.T) scopeFixture {
	t.Helper()
	m := NewManagerAt(t.TempDir(), t.TempDir())
	closed := make(chan *Host, 8)
	recordingClose(m, closed)
	return scopeFixture{
		m:      m,
		closed: closed,
		ws:     pooledFakeHost(m, WorkspaceTarget("/workspace/a"), 0),
		demo:   pooledFakeHost(m, AppTarget("demo"), 0),
		other:  pooledFakeHost(m, AppTarget("other"), 0),
	}
}

// TestInvalidateWorkspacesLeavesApplicationsAlone pins the split of the
// one old InvalidateAll: a workspace reload must leave every
// application alone. Both scopes share one pool, so the only thing
// keeping them apart is the key.
func TestInvalidateWorkspacesLeavesApplicationsAlone(t *testing.T) {
	f := newScopeFixture(t)
	f.m.InvalidateWorkspaces(context.Background())
	assertRetiredHosts(t, f.m, f.closed, f.ws)
	assertPooledHosts(t, f.m, f.closed, f.demo, f.other)
}

// TestInvalidateAppsSelectsByName pins the filter a bare
// InvalidateApps means and the named form's two directions: naming one
// application spares the others, naming none drops all of them and no
// workspace, and naming one the pool does not hold drops nothing at all.
// The two shapes are what production asks of it: the settings save (and
// anything else both scopes read) invalidates every application with no
// name, and the application page names the one it changed.
func TestInvalidateAppsSelectsByName(t *testing.T) {
	t.Run("one named", func(t *testing.T) {
		f := newScopeFixture(t)
		f.m.InvalidateApps(context.Background(), "demo")
		assertRetiredHosts(t, f.m, f.closed, f.demo)
		assertPooledHosts(t, f.m, f.closed, f.other, f.ws)
	})

	t.Run("unknown name", func(t *testing.T) {
		f := newScopeFixture(t)
		f.m.InvalidateApps(context.Background(), "missing")
		assertPooledHosts(t, f.m, f.closed, f.demo, f.other, f.ws)
	})

	t.Run("no name means every application", func(t *testing.T) {
		f := newScopeFixture(t)
		f.m.InvalidateApps(context.Background())
		assertRetiredHosts(t, f.m, f.closed, f.demo, f.other)
		assertPooledHosts(t, f.m, f.closed, f.ws)
	})
}

// TestInvalidateAllAndCloseAllDropBothScopes pins the union the two
// single-scope methods were split out of. It is on the live path:
// Runtime.Close is what calls CloseAll, so a version that quietly
// covered workspaces only would leak every application's runtime on
// shutdown, with nothing else in the suite to say so.
func TestInvalidateAllAndCloseAllDropBothScopes(t *testing.T) {
	t.Run("InvalidateAll", func(t *testing.T) {
		f := newScopeFixture(t)
		f.m.InvalidateAll(context.Background())
		assertRetiredHosts(t, f.m, f.closed, f.ws, f.demo, f.other)
	})

	t.Run("CloseAll", func(t *testing.T) {
		f := newScopeFixture(t)
		f.m.CloseAll()
		assertRetiredHosts(t, f.m, f.closed, f.ws, f.demo, f.other)
	})
}

// TestSameIDServesOneHostPerScope pins the reason the key carries the
// kind, one level above the key's own format: a directory named exactly
// like an application must resolve to its own Host, and neither scope
// may answer for the other.
func TestSameIDServesOneHostPerScope(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	sameID := "/apps/werewolf"
	ws := pooledFakeHost(m, WorkspaceTarget(sameID), 0)
	app := pooledFakeHost(m, AppTarget(sameID), 0)

	if got := m.Current(WorkspaceTarget(sameID)); got != ws {
		t.Fatalf("workspace Current = %p, want %p", got, ws)
	}
	if got := m.Current(AppTarget(sameID)); got != app {
		t.Fatalf("app Current = %p, want %p", got, app)
	}
	if len(m.hosts) != 2 {
		t.Fatalf("pool holds %d entries for two targets with one id", len(m.hosts))
	}
	// Retiring the workspace must not take the application's entry with
	// it: the two are one string and two targets.
	closed := make(chan *Host, 1)
	recordingClose(m, closed)
	m.InvalidateWorkspaces(context.Background())
	if got := m.Current(AppTarget(sameID)); got != app {
		t.Fatalf("app Current after a workspace reload = %p, want %p", got, app)
	}
	select {
	case got := <-closed:
		if got != ws {
			t.Fatalf("closed host = %p, want the workspace's %p", got, ws)
		}
	default:
		t.Fatal("the workspace host was never closed")
	}
}
