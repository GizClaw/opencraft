package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// The assembly record has to survive a real pool: an application that
// assembles, is invalidated and assembles again says so, a package the
// host refuses records the refusal without counting as a runtime, and
// the repair leaves both facts standing — the count that says the
// application is being rebuilt, and the text of what was wrong, which
// is the only copy a user with the YAML open can read.
func TestRuntimeAssembliesAreRecordedWithWhatAskedForThem(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppFixture(t, provider, "")
	target := host.AppTarget("hello")

	acquire := func(reason host.AssemblyReason) (*host.Host, error) {
		t.Helper()
		return f.mgr.Acquire(
			host.WithAssemblyReason(context.Background(), reason),
			target, interact.Auto{}, nil)
	}
	stats := func() host.AssemblyStats {
		t.Helper()
		return f.mgr.AssemblyStats(target)
	}
	release := func(h *host.Host) {
		t.Helper()
		if err := h.Close(); err != nil {
			t.Fatalf("close application host: %v", err)
		}
	}

	h, err := acquire(host.ReasonAppTurn)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	got := stats()
	if got.Count != 1 || got.Last.Reason != host.ReasonAppTurn {
		t.Fatalf("after the first assembly: %+v", got)
	}
	if got.Last.At.IsZero() {
		t.Fatalf("an assembly recorded no moment: %+v", got.Last)
	}
	if got.Last.Failed() || got.LastFailure != (host.AssemblyAttempt{}) {
		t.Fatalf("a clean assembly recorded a refusal: %+v", got)
	}
	release(h)

	// A read brings the runtime back, and that is the reason the record
	// carries: the panel's question is which caller put this application
	// back on its feet.
	f.mgr.InvalidateApps(context.Background(), "hello")
	h, err = acquire(host.ReasonAppRead)
	if err != nil {
		t.Fatalf("acquire after invalidation: %v", err)
	}
	got = stats()
	if got.Count != 2 || got.Last.Reason != host.ReasonAppRead {
		t.Fatalf("after the read's assembly: %+v", got)
	}
	if got.LastFailure != (host.AssemblyAttempt{}) {
		t.Fatalf("a clean assembly recorded a refusal: %+v", got)
	}
	release(h)

	// The breakage an author actually hits: the graph a layer points at
	// is gone, so the assembly refuses and nothing serves the app.
	graph := filepath.Join(f.dataDir, "apps", "hello", "content", "graph.yaml")
	original, err := os.ReadFile(graph)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(graph); err != nil {
		t.Fatal(err)
	}
	f.mgr.InvalidateApps(context.Background(), "hello")
	if _, err := acquire(host.ReasonAppReload); err == nil {
		t.Fatal("an application whose graph is gone assembled")
	} else if !strings.Contains(err.Error(), "graph.yaml") {
		t.Fatalf("the refusal does not name the missing file: %v", err)
	}
	got = stats()
	if got.Count != 2 {
		t.Fatalf("a refusal counted as an assembly: %+v", got)
	}
	if got.Last.Reason != host.ReasonAppReload || !got.Last.Failed() {
		t.Fatalf("the refusal is not the last attempt: %+v", got)
	}
	if got.LastFailure != got.Last {
		t.Fatalf("the refusal is not recorded as a failure: %+v", got)
	}
	if !strings.Contains(got.LastFailure.Err, "graph.yaml") {
		t.Fatalf("the recorded refusal does not name the file: %+v", got.LastFailure)
	}

	// Putting the file back brings the application back, and the record
	// keeps the refusal: someone fixing a package wants to know what it
	// said, not just that it works now.
	if err := os.WriteFile(graph, original, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err = acquire(host.ReasonAppReload)
	if err != nil {
		t.Fatalf("acquire after the repair: %v", err)
	}
	fixed := stats()
	release(h)
	if fixed.Count != 3 || fixed.Last.Reason != host.ReasonAppReload {
		t.Fatalf("after the repair: %+v", fixed)
	}
	if fixed.Last.Failed() {
		t.Fatalf("the repaired assembly recorded a failure: %+v", fixed.Last)
	}
	if fixed.LastFailure != got.LastFailure {
		t.Fatalf("the repair rewrote the refusal: %+v", fixed.LastFailure)
	}
	if !fixed.LastFailure.At.Before(fixed.Last.At) {
		t.Fatalf("the refusal is not older than the assembly that followed "+
			"it: %+v", fixed)
	}
}
