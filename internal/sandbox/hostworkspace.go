package sandbox

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/skills"
	"github.com/GizClaw/opencraft/internal/utils/resourcedep"
)

// HostWorkspace implements workspace.Workspace, delegating to the
// confined LocalWorkspace in workspace mode and to a full-host
// workspace in YOLO mode. The switch is resolved per call from the
// session in the execution context, so a mode change applies to the
// next tool call immediately.
type HostWorkspace struct {
	sessions *sessions.Store
	confined workspace.Workspace
	host     workspace.Workspace
	root     string
	// readonly lists absolute host paths readable (but never writable)
	// in workspace mode: discovered skill roots and configured extras.
	readonly []string
}

func (w *HostWorkspace) pick(ctx context.Context) workspace.Workspace {
	if IsYOLO(ctx, w.sessions) {
		return w.host
	}
	if len(w.readonly) > 0 {
		return &confinedWithReadonly{
			confined: w.confined,
			host:     w.host,
			root:     w.root,
			readonly: w.readonly,
		}
	}
	return w.confined
}

func (w *HostWorkspace) Read(ctx context.Context, path string) ([]byte, error) {
	return w.pick(ctx).Read(ctx, path)
}

func (w *HostWorkspace) Write(
	ctx context.Context, path string, data []byte,
) error {
	return w.pick(ctx).Write(ctx, path, data)
}

func (w *HostWorkspace) Append(
	ctx context.Context, path string, data []byte,
) error {
	return w.pick(ctx).Append(ctx, path, data)
}

func (w *HostWorkspace) Rename(
	ctx context.Context, src, dst string,
) error {
	return w.pick(ctx).Rename(ctx, src, dst)
}

func (w *HostWorkspace) Delete(ctx context.Context, path string) error {
	return w.pick(ctx).Delete(ctx, path)
}

func (w *HostWorkspace) RemoveAll(ctx context.Context, path string) error {
	return w.pick(ctx).RemoveAll(ctx, path)
}

func (w *HostWorkspace) List(
	ctx context.Context, dir string,
) ([]fs.DirEntry, error) {
	return w.pick(ctx).List(ctx, dir)
}

func (w *HostWorkspace) Exists(ctx context.Context, path string) (bool, error) {
	return w.pick(ctx).Exists(ctx, path)
}

func (w *HostWorkspace) Stat(
	ctx context.Context, path string,
) (fs.FileInfo, error) {
	return w.pick(ctx).Stat(ctx, path)
}

var _ workspace.Workspace = (*HostWorkspace)(nil)

// confinedWithReadonly is the workspace-mode view: paths under a
// readonly root are served read-only from the host, everything else
// delegates to the confined LocalWorkspace. Writes never escape to
// readonly roots.
type confinedWithReadonly struct {
	confined workspace.Workspace
	host     workspace.Workspace
	root     string
	readonly []string
}

func (w *confinedWithReadonly) readonlyPath(full string) bool {
	for _, root := range w.readonly {
		if full == root || strings.HasPrefix(full, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (w *confinedWithReadonly) Read(ctx context.Context, path string) ([]byte, error) {
	full, err := coresandbox.Resolve(w.root, path)
	if err != nil {
		return nil, err
	}
	if w.readonlyPath(full) {
		return w.host.Read(ctx, path)
	}
	return w.confined.Read(ctx, path)
}

func (w *confinedWithReadonly) Write(
	ctx context.Context, path string, data []byte,
) error {
	return w.confined.Write(ctx, path, data)
}

func (w *confinedWithReadonly) Append(
	ctx context.Context, path string, data []byte,
) error {
	return w.confined.Append(ctx, path, data)
}

func (w *confinedWithReadonly) Rename(
	ctx context.Context, src, dst string,
) error {
	return w.confined.Rename(ctx, src, dst)
}

func (w *confinedWithReadonly) Delete(ctx context.Context, path string) error {
	return w.confined.Delete(ctx, path)
}

func (w *confinedWithReadonly) RemoveAll(ctx context.Context, path string) error {
	return w.confined.RemoveAll(ctx, path)
}

func (w *confinedWithReadonly) List(
	ctx context.Context, dir string,
) ([]fs.DirEntry, error) {
	full, err := coresandbox.Resolve(w.root, dir)
	if err != nil {
		return nil, err
	}
	if w.readonlyPath(full) {
		return w.host.List(ctx, dir)
	}
	return w.confined.List(ctx, dir)
}

func (w *confinedWithReadonly) Exists(ctx context.Context, path string) (bool, error) {
	full, err := coresandbox.Resolve(w.root, path)
	if err != nil {
		return false, err
	}
	if w.readonlyPath(full) {
		return w.host.Exists(ctx, path)
	}
	return w.confined.Exists(ctx, path)
}

func (w *confinedWithReadonly) Stat(
	ctx context.Context, path string,
) (fs.FileInfo, error) {
	full, err := coresandbox.Resolve(w.root, path)
	if err != nil {
		return nil, err
	}
	if w.readonlyPath(full) {
		return w.host.Stat(ctx, path)
	}
	return w.confined.Stat(ctx, path)
}

var _ workspace.Workspace = (*confinedWithReadonly)(nil)

// hostWorkspace is the YOLO-mode workspace: relative paths resolve
// against the workspace root, absolute paths and escapes are allowed,
// so file tools can reach anywhere on the host.
type hostWorkspace struct {
	root string
}

func (w *hostWorkspace) resolve(path string) (string, error) {
	if path == "" {
		return w.root, nil
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Join(w.root, path), nil
}

func (w *hostWorkspace) Read(_ context.Context, path string) ([]byte, error) {
	full, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", workspace.ErrNotFound, path)
		}
		return nil, fmt.Errorf("workspace: read %s: %w", path, err)
	}
	return data, nil
}

func (w *hostWorkspace) Write(
	_ context.Context, path string, data []byte,
) error {
	full, err := w.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("workspace: mkdir for %s: %w", path, err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return fmt.Errorf("workspace: write %s: %w", path, err)
	}
	return nil
}

func (w *hostWorkspace) Append(
	_ context.Context, path string, data []byte,
) error {
	full, err := w.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("workspace: mkdir for %s: %w", path, err)
	}
	f, err := os.OpenFile(full, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("workspace: append %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("workspace: append %s: %w", path, err)
	}
	return nil
}

func (w *hostWorkspace) Rename(
	_ context.Context, src, dst string,
) error {
	from, err := w.resolve(src)
	if err != nil {
		return err
	}
	to, err := w.resolve(dst)
	if err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", workspace.ErrNotFound, src)
		}
		return fmt.Errorf("workspace: rename %s -> %s: %w", src, dst, err)
	}
	return nil
}

func (w *hostWorkspace) Delete(_ context.Context, path string) error {
	full, err := w.resolve(path)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("workspace: delete %s: %w", path, err)
	}
	return nil
}

func (w *hostWorkspace) RemoveAll(_ context.Context, path string) error {
	full, err := w.resolve(path)
	if err != nil {
		return err
	}
	if full == w.root {
		return errdefs.Validationf(
			"workspace: removing the workspace root is rejected")
	}
	if err := os.RemoveAll(full); err != nil {
		return fmt.Errorf("workspace: remove %s: %w", path, err)
	}
	return nil
}

func (w *hostWorkspace) List(
	_ context.Context, dir string,
) ([]fs.DirEntry, error) {
	full, err := w.resolve(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("workspace: list %s: %w", dir, err)
	}
	return entries, nil
}

func (w *hostWorkspace) Exists(_ context.Context, path string) (bool, error) {
	full, err := w.resolve(path)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(full); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
}

func (w *hostWorkspace) Stat(
	_ context.Context, path string,
) (fs.FileInfo, error) {
	full, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", workspace.ErrNotFound, path)
		}
		return nil, fmt.Errorf("workspace: stat %s: %w", path, err)
	}
	return info, nil
}

var _ workspace.Workspace = (*hostWorkspace)(nil)

// HostWorkspaceSettings configures the confined LocalWorkspace the
// HostWorkspace resource delegates to in workspace mode.
type HostWorkspaceSettings struct {
	Root string `json:"root"`
	// ReadonlyRoots are absolute host paths readable (never writable)
	// in workspace mode. Discovered skill roots are appended by the
	// factory from the optional skills dep.
	ReadonlyRoots []string `json:"readonly_roots,omitempty"`
}

// HostWorkspaceFactory builds the opencraft.hostworkspace resource:
// a mode-aware workspace over the session's persisted permission mode.
type HostWorkspaceFactory struct{}

var _ resource.Factory = HostWorkspaceFactory{}

func (HostWorkspaceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "opencraft.hostworkspace",
		Impl: "local",
		Deps: []resource.DepSpec{
			{Name: "sessions", Type: sessions.ResourceKind, Required: true},
			// Optional: contributes discovered skill roots to the
			// workspace-mode read-only allowlist.
			{Name: "skills", Type: skills.ResourceKind, Required: false},
		},
	}
}

func (HostWorkspaceFactory) New(
	_ context.Context,
	in resource.Input,
) (any, error) {
	settings, err := resource.DecodeTyped[HostWorkspaceSettings](
		in.Settings, resource.ExpandEnv())
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft hostworkspace: decode settings: %v", err)
	}
	if settings.Root == "" {
		return nil, errdefs.Validationf(
			"opencraft hostworkspace: settings.root is required")
	}
	store, err := resourcedep.Required[*sessions.Store](
		in, "opencraft hostworkspace", "sessions")
	if err != nil {
		return nil, err
	}
	confined, err := workspace.NewLocalWorkspace(settings.Root)
	if err != nil {
		return nil, err
	}
	readonly := append([]string(nil), settings.ReadonlyRoots...)
	if dep, ok := in.Dep("skills"); ok {
		if svc, ok := dep.(*skills.Service); ok {
			readonly = append(readonly, svc.Roots()...)
		}
	}
	return &HostWorkspace{
		sessions: store,
		confined: confined,
		host:     &hostWorkspace{root: settings.Root},
		root:     settings.Root,
		readonly: dedupePaths(readonly),
	}, nil
}

func dedupePaths(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		clean := filepath.Clean(abs)
		if real, err := coresandbox.EvalExistingPrefix(clean); err == nil {
			clean = real
		}
		if clean == "" || clean == "." || seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out
}
