package sandbox

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// captureObserver records every observed write for assertions.
type captureObserver struct {
	*ArtifactObserver
	mu   sync.Mutex
	seen []observedWrite
}

type observedWrite struct {
	path string
	data []byte
}

func newCaptureObserver() *captureObserver {
	c := &captureObserver{ArtifactObserver: &ArtifactObserver{}}
	c.SetSink(func(_ context.Context, path string, data []byte) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.seen = append(c.seen, observedWrite{path: path, data: data})
	})
	return c
}

func (c *captureObserver) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seen)
}

func (c *captureObserver) last() observedWrite {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seen) == 0 {
		return observedWrite{}
	}
	return c.seen[len(c.seen)-1]
}

// TestObservingWorkspaceReportsSessionWrites verifies the RunInfo
// filter, the Write/Append/Rename hooks, and Root forwarding.
func TestObservingWorkspaceReportsSessionWrites(t *testing.T) {
	root := t.TempDir()
	inner, err := workspace.NewLocalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	c := newCaptureObserver()
	ow := &observingWorkspace{inner: inner, obs: c.ArtifactObserver}
	t.Cleanup(func() { _ = ow.Close() })

	if ow.Root() != inner.Root() {
		t.Fatalf("Root() = %q, want %q", ow.Root(), inner.Root())
	}

	// System writes (no RunInfo) are not reported.
	if err := ow.Write(context.Background(), "sys.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if c.count() != 0 {
		t.Fatalf("system write reported %d times, want 0", c.count())
	}

	// Session writes are reported: Write, Append, Rename.
	if err := ow.Write(sessionCtx("s1"), "a.md", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := c.last(); got.path != "a.md" || string(got.data) != "hello" {
		t.Fatalf("write observed %+v, want a.md/hello", got)
	}
	if err := ow.Append(sessionCtx("s1"), "a.md", []byte(" world")); err != nil {
		t.Fatal(err)
	}
	if got := c.last(); got.path != "a.md" || string(got.data) != " world" {
		t.Fatalf("append observed %+v, want a.md/' world'", got)
	}
	if err := ow.Rename(sessionCtx("s1"), "a.md", "docs/final.md"); err != nil {
		t.Fatal(err)
	}
	if got := c.last(); got.path != "docs/final.md" || got.data != nil {
		t.Fatalf("rename observed %+v, want docs/final.md/nil data", got)
	}
	if c.count() != 3 {
		t.Fatalf("observed %d writes, want 3", c.count())
	}
}

// TestObservingWorkspaceBoundedReadAndClose verifies the wrapper can
// satisfy workspace.LimitedReader (required by core's fs bridge) and
// forwards Close to the inner LocalWorkspace's pinned root handle.
func TestObservingWorkspaceBoundedReadAndClose(t *testing.T) {
	root := t.TempDir()
	inner, err := workspace.NewLocalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ow := &observingWorkspace{inner: inner}

	if err := ow.Write(context.Background(), "large.txt", []byte("hello world")); err != nil {
		t.Fatal(err)
	}
	data, err := ow.ReadLimited(context.Background(), "large.txt", 64)
	if err != nil {
		t.Fatalf("ReadLimited: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("ReadLimited = %q", data)
	}
	if err := ow.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestObservingWorkspaceFactoryBuildsSharedWS verifies the
// opencraft.workspace factory wraps the LocalWorkspace and shares the
// artifact sink.
func TestObservingWorkspaceFactoryBuildsSharedWS(t *testing.T) {
	root := t.TempDir()
	c := newCaptureObserver()
	value, err := (ObservingWorkspaceFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{"root":` + strconv.Quote(root) + `}`),
		Deps: map[string]any{
			"observer": c.ArtifactObserver,
		},
	})
	if err != nil {
		t.Fatalf("factory new: %v", err)
	}
	ow, ok := value.(*observingWorkspace)
	if !ok {
		t.Fatalf("factory returned %T, want *observingWorkspace", value)
	}
	inner, ok := ow.inner.(*workspace.LocalWorkspace)
	if !ok {
		t.Fatalf("inner is %T, want *LocalWorkspace", ow.inner)
	}
	if inner.Root() != ow.Root() {
		t.Fatalf("Root() = %q, want %q", ow.Root(), inner.Root())
	}
	if err := ow.Write(sessionCtx("s1"), "doc.md", []byte("body")); err != nil {
		t.Fatal(err)
	}
	if c.count() != 1 {
		t.Fatalf("observed %d writes, want 1", c.count())
	}
	t.Cleanup(func() { _ = ow.Close() })
}

// TestHostWorkspaceFactoryWrapsBothBranches verifies both write paths
// report through the shared artifact sink: workspace mode via the
// confined ws, YOLO mode via the wrapped host branch.
func TestHostWorkspaceFactoryWrapsBothBranches(t *testing.T) {
	root := t.TempDir()
	c := newCaptureObserver()
	wsValue, err := (ObservingWorkspaceFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{"root":` + strconv.Quote(root) + `}`),
		Deps: map[string]any{
			"observer": c.ArtifactObserver,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t)
	hwValue, err := (HostWorkspaceFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{}`),
		Deps: map[string]any{
			"sessions":  store,
			"workspace": wsValue,
			"observer":  c.ArtifactObserver,
		},
	})
	if err != nil {
		t.Fatalf("hostws factory new: %v", err)
	}
	hw := hwValue.(*HostWorkspace)

	// Workspace mode: write inside the root goes through the confined
	// (observing) ws and is reported.
	if err := hw.Write(sessionCtx("s1"), "inside.md", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if c.count() != 1 {
		t.Fatalf("workspace-mode write observed %d times, want 1", c.count())
	}

	// YOLO mode: write outside the root goes through the wrapped host
	// branch and is reported; without RunInfo it is not.
	if err := store.SetMode(context.Background(), "s1", sessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := hw.Write(sessionCtx("s1"), outside, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if c.count() != 2 {
		t.Fatalf("yolo write observed %d times, want 2", c.count())
	}
	if got := c.last(); got.path != outside {
		t.Fatalf("yolo write path = %q, want %q", got.path, outside)
	}
	// A run-less write (no RunInfo) stays confined and is not reported,
	// even while the session is in YOLO mode.
	if err := hw.Write(context.Background(), "inside2.md", []byte("z")); err != nil {
		t.Fatal(err)
	}
	if c.count() != 2 {
		t.Fatalf("run-less yolo write observed %d times, want still 2", c.count())
	}
}
