package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
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
	first, err := c.Runtime.EnsureHost(ctx, host.WorkspaceTarget(workDir))
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

// invalidationsWithReason counts the runtime invalidations that carried
// one assembly reason, which is where the refresh path's reason lands.
func invalidationsWithReason(recorder *logcapture.Recorder, reason string) int {
	count := 0
	for _, record := range recorder.Records() {
		if record.Body().AsString() != "host: runtime invalidated" {
			continue
		}
		if logcapture.Attribute(record, "reason") == reason {
			count++
		}
	}
	return count
}

// TestRefreshPluginRuntimeCoalescesConcurrentCallers pins what the mutex
// is for: callers that arrive while one refresh is already rebuilding
// wait for it and find the revision covered, so a burst costs one
// rebuild. The earlier test staged the burst sequentially and called
// refresh once, so dropping the lock left it green.
//
// The first caller is held inside its rebuild (the ready event fires
// from there, under the lock) until every other caller has arrived.
func TestRefreshPluginRuntimeCoalescesConcurrentCallers(t *testing.T) {
	c, workDir, _ := newInstallerTestCore(t)
	ctx := context.Background()
	if _, err := c.Runtime.EnsureHost(ctx, host.WorkspaceTarget(workDir)); err != nil {
		t.Fatalf("ensure host: %v", err)
	}
	src := filepath.Join(workDir, ".opencraft-plugins", "hello")
	writePluginSource(t, src, "hello", "0.1.0")
	if _, err := c.Plugin.Store.Install(src); err != nil {
		t.Fatalf("install: %v", err)
	}

	inside := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	c.Shell.SetNotificationSink(func(typ string, _ any) {
		if typ != EventReady {
			return
		}
		once.Do(func() {
			close(inside)
			<-release
		})
	})

	recorder := logcapture.Install(t)
	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- c.RefreshPluginRuntime(ctx)
	}()
	select {
	case <-inside:
	case <-time.After(30 * time.Second):
		t.Fatal("the first refresh never reached its rebuild")
	}
	for i := 1; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.RefreshPluginRuntime(ctx)
		}()
	}
	// Give the followers time to arrive at the lock before the holder
	// releases it; without the lock they rebuild here instead.
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent refresh: %v", err)
		}
	}

	if got := countInvalidations(recorder); got != 1 {
		t.Fatalf("eight concurrent refreshes invalidated the runtime "+
			"%d times, want 1", got)
	}
	if got := invalidationsWithReason(recorder, "plugin_change"); got != 1 {
		t.Fatalf("invalidations carrying reason=plugin_change = %d, want 1", got)
	}
}

// TestRefreshPluginRuntimeFoldsAMutationInsideTheRebuild pins the
// trailing pass. The ready event is emitted from inside RebuildRuntime,
// which the refresh runs under its lock, so mutating there lands
// mid-rebuild — after the revision was read, before it is re-read. No
// caller refreshes afterwards: only the trailing pass can bring the
// runtime to the revision the mutation left behind, so replacing the
// loop with a single rebuild (or dropping the re-read) fails here.
func TestRefreshPluginRuntimeFoldsAMutationInsideTheRebuild(t *testing.T) {
	c, workDir, _ := newInstallerTestCore(t)
	ctx := context.Background()
	if _, err := c.Runtime.EnsureHost(ctx, host.WorkspaceTarget(workDir)); err != nil {
		t.Fatalf("ensure host: %v", err)
	}
	first := filepath.Join(workDir, ".opencraft-plugins", "hello")
	writePluginSource(t, first, "hello", "0.1.0")
	if _, err := c.Plugin.Store.Install(first); err != nil {
		t.Fatalf("install hello: %v", err)
	}
	late := filepath.Join(workDir, ".opencraft-plugins", "late")
	writePluginSource(t, late, "late", "0.1.0")

	var once sync.Once
	c.Shell.SetNotificationSink(func(typ string, _ any) {
		if typ != EventReady {
			return
		}
		once.Do(func() {
			if _, err := c.Plugin.Store.Install(late); err != nil {
				t.Errorf("install during the rebuild: %v", err)
			}
		})
	})

	recorder := logcapture.Install(t)
	if err := c.RefreshPluginRuntime(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := countInvalidations(recorder); got != 2 {
		t.Fatalf("invalidations = %d, want 2: the rebuild and the trailing "+
			"pass that folds in the mutation that landed inside it", got)
	}

	// The runtime that ends up installed carries both plugins, so the
	// trailing pass rebuilt at the new revision rather than repeating
	// the first one.
	third := c.ActiveHost()
	if third == nil {
		t.Fatal("no active host after the refresh")
	}
	value, ok := third.Controller().Runtime().Resource("skills")
	if !ok {
		t.Fatal("skills resource missing after the refresh")
	}
	svc, ok := value.(*skills.Service)
	if !ok || svc == nil {
		t.Fatal("skills resource is not *skills.Service")
	}
	// Both plugins contribute a skill from their own directory, so the
	// paths — not the names, which the fixture reuses — say which
	// registry state this assembly read.
	paths := make([]string, 0, 2)
	for _, sk := range svc.List() {
		if strings.Contains(sk.Path, "plugins/") {
			paths = append(paths, filepath.ToSlash(sk.Path))
		}
	}
	sort.Strings(paths)
	if len(paths) != 2 ||
		!strings.HasSuffix(paths[0], "plugins/hello/skills/hello/SKILL.md") ||
		!strings.HasSuffix(paths[1], "plugins/late/skills/hello/SKILL.md") {
		t.Fatalf("plugin skills after the folded refresh = %v, want the "+
			"plugins installed before and during the rebuild", paths)
	}
}

// TestRefreshPluginRuntimeBoundsTheTrailingPasses pins the cap on the
// coalescing loop. A mutation landing in every rebuild is what a stream
// of installs looks like from here, and the unbounded loop answers it
// with one full assembly per mutation for as long as the stream lasts:
// four rebuilds in this test's shape, and no end at all for a stream
// that does not stop. The capped loop rebuilds twice — the caller's
// revision and one trailing pass — and hands the rest to the next
// caller, which is what the follow-up refresh below is.
func TestRefreshPluginRuntimeBoundsTheTrailingPasses(t *testing.T) {
	c, workDir, _ := newInstallerTestCore(t)
	ctx := context.Background()
	if _, err := c.Runtime.EnsureHost(ctx, host.WorkspaceTarget(workDir)); err != nil {
		t.Fatalf("ensure host: %v", err)
	}
	first := filepath.Join(workDir, ".opencraft-plugins", "hello")
	writePluginSource(t, first, "hello", "0.1.0")
	if _, err := c.Plugin.Store.Install(first); err != nil {
		t.Fatalf("install hello: %v", err)
	}

	// Each rebuild stages the next plugin from inside the rebuild, for
	// three passes: one more than the cap, so a loop without it would
	// keep going after the capped one stopped.
	var staged atomic.Int32
	c.Shell.SetNotificationSink(func(typ string, _ any) {
		if typ != EventReady {
			return
		}
		n := staged.Add(1)
		if n > 3 {
			return
		}
		id := fmt.Sprintf("staged-%d", n)
		src := filepath.Join(workDir, ".opencraft-plugins", id)
		writePluginSource(t, src, id, "0.1.0")
		if _, err := c.Plugin.Store.Install(src); err != nil {
			t.Errorf("install %s during the rebuild: %v", id, err)
		}
	})

	recorder := logcapture.Install(t)
	if err := c.RefreshPluginRuntime(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := countInvalidations(recorder); got != 2 {
		t.Fatalf("one refresh across a stream of mutations invalidated the "+
			"runtime %d times, want 2 (the caller's revision and one "+
			"trailing pass)", got)
	}

	// The revision that landed during the capped pass is still ahead, so
	// the next refresh finishes the job. It too has work landing inside
	// it (the sink stages its third plugin from there), so it spends its
	// own two passes: the revision the first call left behind, and the
	// one this call's first pass created.
	if err := c.RefreshPluginRuntime(ctx); err != nil {
		t.Fatalf("refresh after the capped passes: %v", err)
	}
	if got := countInvalidations(recorder); got != 4 {
		t.Fatalf("the follow-up refresh invalidated the runtime %d times "+
			"in total, want 4", got)
	}
	if err := c.RefreshPluginRuntime(ctx); err != nil {
		t.Fatalf("settled refresh: %v", err)
	}
	if got := countInvalidations(recorder); got != 4 {
		t.Fatalf("a settled registry invalidated the runtime again: %d", got)
	}

	// The assembly that ends up installed carries every plugin, so the
	// capped call left the runtime behind, not broken.
	host := c.ActiveHost()
	if host == nil {
		t.Fatal("no active host after the refresh")
	}
	value, ok := host.Controller().Runtime().Resource("skills")
	if !ok {
		t.Fatal("skills resource missing after the refresh")
	}
	svc, ok := value.(*skills.Service)
	if !ok || svc == nil {
		t.Fatal("skills resource is not *skills.Service")
	}
	roots := 0
	for _, sk := range svc.List() {
		if strings.Contains(filepath.ToSlash(sk.Path), "plugins/staged-") {
			roots++
		}
	}
	if roots != 3 {
		t.Fatalf("plugin skills after the settled refresh = %d staged "+
			"roots, want 3: %v", roots, svc.Roots())
	}
}
