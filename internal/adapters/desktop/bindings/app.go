package bindings

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/apps"
	"github.com/GizClaw/opencraft/internal/foundation/utils/filetype"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// App exposes the application platform to the application page: the
// registry (what is installed, what a package would do, install and
// enable it), the runtime behind one application, and the reads the page
// does — its content files (the frontend module it imports, the
// stylesheet it injects, its icons) and its private workspace.
//
// Every method here answers for one installed application rather than
// for the window: an application owns its own Host, its own state root
// and its own conversation ids, so nothing in this file touches the
// active workspace.
type App struct {
	core *core.Core
}

// NewAppBinding wires the application binding.
func NewAppBinding(c *core.Core) *App {
	return &App{core: c}
}

// store returns the registry this launch built. A launch without an app
// home has none, and every method says so instead of guessing a root.
func (b *App) store() (*apps.Store, error) {
	store := b.core.Runtime.Apps()
	if store == nil {
		return nil, errors.New("apps: this launch has no application root")
	}
	return store, nil
}

// appFileReadLimit caps one file read out of an application's private
// workspace. It is the viewer's text ceiling: the page renders source and
// Markdown, and a file past this belongs in the system app.
const appFileReadLimit = 2 << 20

// AppInstallOptions is the import wizard's form as it crosses the wire:
// the manifest fields a user may edit before anything is copied.
//
// An empty id or name means "install it as written" — a manifest cannot
// hold either — while the icon is a pointer so the form can tell "leave
// it alone" (absent) from "the user cleared it" (""). A cleared icon is
// how an application that does not want one says so.
type AppInstallOptions struct {
	ID   string  `json:"id,omitempty"`
	Name string  `json:"name,omitempty"`
	Icon *string `json:"icon,omitempty"`
}

// options converts the wire form into the registry's own.
func (o AppInstallOptions) options() apps.InstallOptions {
	return apps.InstallOptions{ID: o.ID, Name: o.Name, Icon: o.Icon}
}

// List returns every installed application, the cards the page renders.
func (b *App) List() ([]apps.Summary, error) {
	store, err := b.store()
	if err != nil {
		return nil, err
	}
	return store.List()
}

// Inspect reads one candidate package without installing it: the card it
// would render as, its layers, and every reason the preflight refuses it
// — as rows, so the wizard can list what the user has to fix.
func (b *App) Inspect(path string) (apps.Inspection, error) {
	store, err := b.store()
	if err != nil {
		return apps.Inspection{}, err
	}
	return store.Inspect(b.core.Shell.Context(), path)
}

// Install copies a package directory into the registry and enables it.
func (b *App) Install(
	src string, opts AppInstallOptions,
) (apps.Summary, error) {
	store, err := b.store()
	if err != nil {
		return apps.Summary{}, err
	}
	sum, err := store.Install(b.core.Shell.Context(), src, opts.options())
	if err != nil {
		return apps.Summary{}, err
	}
	b.changed(sum.ID)
	return sum, nil
}

// InstallZip installs an application from a zip package (a release
// artifact, an exported folder). The archive layout is the plugin one:
// app.yaml may sit at the root or under a single top-level directory.
func (b *App) InstallZip(
	zipPath string, opts AppInstallOptions,
) (apps.Summary, error) {
	store, err := b.store()
	if err != nil {
		return apps.Summary{}, err
	}
	sum, err := store.InstallZip(b.core.Shell.Context(), zipPath, opts.options())
	if err != nil {
		return apps.Summary{}, err
	}
	b.changed(sum.ID)
	return sum, nil
}

// SetEnabled turns one application on or off, and is where "installed but
// broken" is caught.
//
// Enabling runs the preflight over the application as it now sits on
// disk — the files may have changed since the install — and then really
// assembles it once, so a package the host cannot serve fails here, with
// the error the page shows, instead of at the first message. A failed
// enable writes the disabled state — an install arrives enabled, so the
// flag has to be turned back off — and hands the reason to the card; a
// successful one has a Host ready to take a turn.
//
// Disabling invalidates the application's runtime and closes it when it
// is idle. A runtime with work in flight drains first: the pool retires
// it, and whether a replacement comes back is the pool's question — for
// a disabled application the answer is no (see the desktop's
// replacement policy).
func (b *App) SetEnabled(id string, enabled bool) error {
	store, err := b.store()
	if err != nil {
		return err
	}
	ctx := b.core.Shell.Context()
	app, err := store.Get(id)
	if err != nil {
		return err
	}
	if !enabled {
		if err := store.SetEnabled(id, false); err != nil {
			return err
		}
		if err := b.core.Runtime.ReloadApps(ctx, id); err != nil {
			return err
		}
		b.changed(id)
		return nil
	}
	if err := apps.Validate(ctx, app); err != nil {
		return b.refuseEnable(ctx, store, id, err)
	}
	if err := store.SetEnabled(id, true); err != nil {
		return err
	}
	if _, err := b.core.Runtime.EnsureHost(ctx, host.AppTarget(id)); err != nil {
		return b.refuseEnable(ctx, store, id, err)
	}
	b.changed(id)
	return nil
}

// refuseEnable is the one way out of a failed enable: the application is
// unusable as it stands, so it is left disabled — an install arrives
// enabled, so "disabled" is something the registry has to be told — and
// any Host serving it is invalidated before the reason goes back to the
// page. The original error is what the caller sees; a failure to write
// the disabled state rides along, because a page that shows the first
// error and an application that stays enabled is the worse outcome.
func (b *App) refuseEnable(
	ctx context.Context, store *apps.Store, id string, cause error,
) error {
	if err := store.SetEnabled(id, false); err != nil {
		return fmt.Errorf("%w (and disabling it again failed: %v)", cause, err)
	}
	if err := b.core.Runtime.ReloadApps(ctx, id); err != nil {
		return fmt.Errorf("%w (and unloading it again failed: %v)", cause, err)
	}
	b.changed(id)
	return cause
}

// Uninstall removes one installed application's content. purge also
// removes its state root — its conversations and its private workspace —
// which is the second confirmation the page asks for, never the default.
func (b *App) Uninstall(id string, purge bool) error {
	store, err := b.store()
	if err != nil {
		return err
	}
	if err := store.Uninstall(id, purge); err != nil {
		return err
	}
	// The runtime has to go whichever half was removed: what it reads is
	// gone. An idle one closes now; one with work in flight drains and
	// is not replaced.
	if err := b.core.Runtime.ReloadApps(b.core.Shell.Context(), id); err != nil {
		return err
	}
	b.changed(id)
	return nil
}

// Reload reassembles one application from what its content root now
// holds — an updated or rolled-back package, a layer edited in place
// while the application runs — and reports whether the application is
// servable afterwards. It is the page's "apply this change" call and the
// development loop's, not a turn path: a Host with work in flight drains
// first and is replaced when it ends.
//
// A disabled application reloads to nothing (there is no runtime to
// bring back); an enabled one that cannot assemble fails here, with the
// error the card shows.
func (b *App) Reload(id string) error {
	store, err := b.store()
	if err != nil {
		return err
	}
	if _, err := store.Get(id); err != nil {
		return err
	}
	if err := b.core.ReloadApp(b.core.Shell.Context(), id); err != nil {
		return err
	}
	b.changed(id)
	return nil
}

// AppStatus is what the page's card and the diagnostics view read about
// one application's runtime: the state it is in, whether a Host serves
// it right now, and the three roots involved.
type AppStatus struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Builtin bool   `json:"builtin"`
	// Serving reports a Host that can take work for this application
	// right now; Retiring reports one a reload has retired, which is on
	// its way out but still takes work until its last run ends.
	Serving  bool `json:"serving"`
	Retiring bool `json:"retiring"`
	// ContentRoot is the installed package (read-only at runtime);
	// StateRoot holds sessions, cache and audit; WorkDir is the private
	// workspace the page browses and the runtime writes.
	ContentRoot string `json:"content_root"`
	StateRoot   string `json:"state_root"`
	WorkDir     string `json:"work_dir"`
}

// Status reports one installed application's runtime state. It is the
// card's status dot and the diagnostics panel's first three lines.
func (b *App) Status(id string) (AppStatus, error) {
	store, err := b.store()
	if err != nil {
		return AppStatus{}, err
	}
	app, err := store.Get(id)
	if err != nil {
		return AppStatus{}, err
	}
	stateRoot, err := store.StateRoot(id)
	if err != nil {
		return AppStatus{}, err
	}
	workDir, err := store.WorkDir(id)
	if err != nil {
		return AppStatus{}, err
	}
	out := AppStatus{
		ID:          app.ID,
		Name:        app.Name,
		Enabled:     app.Enabled,
		Builtin:     app.Builtin,
		ContentRoot: app.ContentDir,
		StateRoot:   stateRoot,
		WorkDir:     workDir,
	}
	h := b.core.Runtime.HostFor(host.AppTarget(id))
	if h != nil {
		// The two flags are separate questions, and a reload with work
		// in flight answers them differently: the retired generation
		// keeps taking turns until its runs end (so the application is
		// still being served), and its successor is assembled when the
		// drain ends (so a rebuild is pending). Reading IsClosing for
		// both would call that state "not serving, not retiring" — the
		// card would show an application that is running fine as down.
		out.Serving = !h.IsClosing()
		out.Retiring = h.IsStale()
	}
	return out, nil
}

// AppAsset is one file out of an application's content root as the page
// receives it: base64 bytes (the webview cannot fetch a wails:// or
// dev-server URL for a path outside the frontend bundle), the media type
// the host read off the content, and its size.
type AppAsset struct {
	Data      string `json:"data"`
	MediaType string `json:"media_type"`
	Size      int    `json:"size"`
}

// Asset returns one file out of an installed application's content root,
// confined to it the way the preflight confines the manifest's own
// references. It is how the page loads the frontend module it imports
// and the stylesheet it injects, and how an application reads an icon or
// an image it ships.
func (b *App) Asset(id, rel string) (AppAsset, error) {
	store, err := b.store()
	if err != nil {
		return AppAsset{}, err
	}
	data, err := store.ReadAsset(id, rel)
	if err != nil {
		return AppAsset{}, err
	}
	kind := filetype.OfData(rel, data)
	return AppAsset{
		Data:      base64.StdEncoding.EncodeToString(data),
		MediaType: kind.MediaType,
		Size:      len(data),
	}, nil
}

// ListFiles returns one directory level of an application's private
// workspace, dirs first. An application that has not run yet has no
// workspace, which reads as an empty directory rather than as an error:
// the page shows "nothing here yet" either way.
//
// The listing stops at the workspace on purpose. An application's
// sessions, cache and audit live beside it under the state root, and
// they are the host's business — the page reaches a conversation through
// the API, not through the filesystem.
func (b *App) ListFiles(id, rel string) ([]FileNode, error) {
	store, err := b.store()
	if err != nil {
		return nil, err
	}
	workDir, err := store.WorkDir(id)
	if err != nil {
		return nil, err
	}
	full, err := resolveInWorkDir(workDir, rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(full)
	if errors.Is(err, fs.ErrNotExist) {
		return []FileNode{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]FileNode, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.ToSlash(filepath.Join(filepath.Clean(rel), entry.Name()))
		if rel == "" {
			path = entry.Name()
		}
		out = append(out, FileNode{
			Name:  entry.Name(),
			Path:  path,
			IsDir: entry.IsDir(),
			Size:  info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// ReadFile returns one text file out of an application's private
// workspace. Binary files are refused rather than streamed: the page
// renders text, and a file the viewer cannot render belongs in the
// system app (Reveal opens the folder).
func (b *App) ReadFile(id, rel string) (string, error) {
	store, err := b.store()
	if err != nil {
		return "", err
	}
	workDir, err := store.WorkDir(id)
	if err != nil {
		return "", err
	}
	full, err := resolveInWorkDir(workDir, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("apps: %s: no such file in the workspace", rel)
	case err != nil:
		return "", err
	case info.IsDir():
		return "", fmt.Errorf("apps: %s is a directory", rel)
	case info.Mode()&os.ModeSymlink != 0:
		return "", fmt.Errorf("apps: %s is a symbolic link", rel)
	case !info.Mode().IsRegular():
		return "", fmt.Errorf("apps: %s is not a regular file", rel)
	case info.Size() > appFileReadLimit:
		return "", fmt.Errorf(
			"apps: %s is %d bytes, over the %d byte limit",
			rel, info.Size(), appFileReadLimit)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	if !filetype.IsText(data) {
		return "", fmt.Errorf("apps: %s is not a text file", rel)
	}
	return string(data), nil
}

// Reveal shows one application path in the platform file manager: the
// private workspace itself when rel is empty, the file or directory
// otherwise. It is the page's "open folder" affordance.
func (b *App) Reveal(id, rel string) error {
	store, err := b.store()
	if err != nil {
		return err
	}
	workDir, err := store.WorkDir(id)
	if err != nil {
		return err
	}
	full, err := resolveInWorkDir(workDir, rel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(full); err != nil {
		return err
	}
	return openWith(b.core.Shell.Context(), runtime.GOOS, reveal, full)
}

// changed tells the page that one application was installed, enabled,
// disabled or removed, so the list and the card reload.
func (b *App) changed(id string) {
	b.core.Shell.Emit(core.EventAppChanged, core.AppChangedEvent{ID: id})
}

// resolveInWorkDir resolves one slash-spelled relative reference inside
// an application's private workspace and refuses anything that leaves it:
// a path that walks out, an absolute path, or a directory on the way that
// is a link to somewhere else. The empty reference is the workspace
// itself.
func resolveInWorkDir(workDir, rel string) (string, error) {
	trimmed := strings.TrimSpace(rel)
	if trimmed == "" {
		return filepath.Clean(workDir), nil
	}
	if filepath.IsAbs(trimmed) || !pathsafe.RelRef(trimmed) {
		return "", fmt.Errorf(
			"apps: %q must be a relative path inside the workspace", rel)
	}
	full, err := pathsafe.ResolveUnder(workDir, trimmed)
	if err != nil {
		return "", fmt.Errorf("apps: %q escapes the workspace", rel)
	}
	if !pathsafe.RealWithin(workDir, full) {
		return "", fmt.Errorf("apps: %q resolves outside the workspace", rel)
	}
	return full, nil
}
