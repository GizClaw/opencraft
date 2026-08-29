package sandbox

import (
	"context"
	"io/fs"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/workspace"
)

// WriteObserver receives one successful workspace write (or rename
// publish). data carries the written bytes; Rename passes nil.
type WriteObserver func(ctx context.Context, path string, data []byte)

// ArtifactObserver is the shared sink for observingWorkspace instances.
// The desktop installs a sink that re-emits artifact UI events; without
// a sink, writes are observed into the void.
type ArtifactObserver struct {
	mu   sync.RWMutex
	sink WriteObserver
}

// SetSink installs or clears (nil) the write observer.
func (o *ArtifactObserver) SetSink(fn WriteObserver) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sink = fn
}

func (o *ArtifactObserver) notify(ctx context.Context, path string, data []byte) {
	o.mu.RLock()
	fn := o.sink
	o.mu.RUnlock()
	if fn != nil {
		fn(ctx, path, data)
	}
}

// ArtifactObserverResourceKind is the deploy resource kind for the
// shared artifact sink.
const ArtifactObserverResourceKind = "opencraft.artifacts"

// ArtifactObserverFactory builds the opencraft.artifacts resource.
type ArtifactObserverFactory struct{}

var _ resource.Factory = ArtifactObserverFactory{}

func (ArtifactObserverFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ArtifactObserverResourceKind,
		Impl: "local",
	}
}

func (ArtifactObserverFactory) New(
	context.Context, resource.Input,
) (any, error) {
	return &ArtifactObserver{}, nil
}

// observingWorkspace wraps a workspace.Workspace and reports successful
// Write / Append / Rename calls that happen inside a session run
// (RunInfo carrying a conversation id). System writes without RunInfo
// are not reported, so "current turn" artifacts only count agent tool
// output.
type observingWorkspace struct {
	inner workspace.Workspace
	obs   *ArtifactObserver
}

var _ workspace.Workspace = (*observingWorkspace)(nil)

func (w *observingWorkspace) notify(ctx context.Context, path string, data []byte) {
	if w.obs == nil {
		return
	}
	info, ok := agent.RunInfoFromContext(ctx)
	if !ok || info.ConversationID == "" {
		return
	}
	w.obs.notify(ctx, path, data)
}

// Root forwards the inner workspace's root so the hostws factory can
// keep deriving its host/YOLO resolution root from the shared ws.
func (w *observingWorkspace) Root() string {
	if r, ok := w.inner.(interface{ Root() string }); ok {
		return r.Root()
	}
	return ""
}

func (w *observingWorkspace) Read(
	ctx context.Context, path string,
) ([]byte, error) {
	return w.inner.Read(ctx, path)
}

func (w *observingWorkspace) Write(
	ctx context.Context, path string, data []byte,
) error {
	if err := w.inner.Write(ctx, path, data); err != nil {
		return err
	}
	w.notify(ctx, path, data)
	return nil
}

func (w *observingWorkspace) Append(
	ctx context.Context, path string, data []byte,
) error {
	if err := w.inner.Append(ctx, path, data); err != nil {
		return err
	}
	w.notify(ctx, path, data)
	return nil
}

func (w *observingWorkspace) Rename(
	ctx context.Context, src, dst string,
) error {
	if err := w.inner.Rename(ctx, src, dst); err != nil {
		return err
	}
	w.notify(ctx, dst, nil)
	return nil
}

func (w *observingWorkspace) Delete(
	ctx context.Context, path string,
) error {
	return w.inner.Delete(ctx, path)
}

func (w *observingWorkspace) RemoveAll(
	ctx context.Context, path string,
) error {
	return w.inner.RemoveAll(ctx, path)
}

func (w *observingWorkspace) List(
	ctx context.Context, dir string,
) ([]fs.DirEntry, error) {
	return w.inner.List(ctx, dir)
}

func (w *observingWorkspace) Exists(
	ctx context.Context, path string,
) (bool, error) {
	return w.inner.Exists(ctx, path)
}

func (w *observingWorkspace) Stat(
	ctx context.Context, path string,
) (fs.FileInfo, error) {
	return w.inner.Stat(ctx, path)
}

// ObservingWorkspaceResourceKind is the deploy resource kind for the
// shared observing workspace (the ws resource).
const ObservingWorkspaceResourceKind = "opencraft.workspace"

// ObservingWorkspaceSettings configures the opencraft.workspace
// resource.
type ObservingWorkspaceSettings struct {
	Root string `json:"root"`
}

// ObservingWorkspaceFactory builds the shared observing workspace: a
// confined LocalWorkspace whose successful writes are reported to the
// artifact sink.
type ObservingWorkspaceFactory struct{}

var _ resource.Factory = ObservingWorkspaceFactory{}

func (ObservingWorkspaceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ObservingWorkspaceResourceKind,
		Impl: "local",
		Deps: []resource.DepSpec{
			// Optional: the shared artifact sink. Without it writes are
			// still confined but not reported.
			{
				Name:     "observer",
				Type:     ArtifactObserverResourceKind,
				Required: false,
			},
		},
	}
}

func (ObservingWorkspaceFactory) New(
	ctx context.Context,
	in resource.Input,
) (any, error) {
	settings, err := resource.DecodeTyped[ObservingWorkspaceSettings](
		ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft workspace: decode settings: %v", err)
	}
	if settings.Root == "" {
		return nil, errdefs.Validationf(
			"opencraft workspace: settings.root is required")
	}
	inner, err := workspace.NewLocalWorkspace(settings.Root)
	if err != nil {
		return nil, err
	}
	obs := &ArtifactObserver{}
	if dep, ok := in.Dep("observer"); ok {
		if o, ok := dep.(*ArtifactObserver); ok {
			obs = o
		}
	}
	return &observingWorkspace{inner: inner, obs: obs}, nil
}
