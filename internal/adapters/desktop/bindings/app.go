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
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"
	flowtelemetry "github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/apps"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
	"github.com/GizClaw/opencraft/internal/foundation/utils/filetype"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
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
	// kvMu guards kvStores: one KV store per application, cached so
	// concurrent writes serialize on the same mutex (a fresh store per
	// call would give each caller its own lock and let two writers
	// interleave read-modify-write).
	kvMu     sync.Mutex
	kvStores map[string]*plugins.KVStore
}

// NewAppBinding wires the application binding.
func NewAppBinding(c *core.Core) *App {
	return &App{core: c, kvStores: map[string]*plugins.KVStore{}}
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

// Update replaces an installed application's content with a newer package
// and serves it: the registry swaps the content root (the previous version
// is snapshotted, so Rollback is one click away), and the application is
// assembled again on the new bytes.
//
// A version the host cannot serve fails here rather than at the first
// message — the package passed the preflight, so what is left to fail is
// the assembly — and the message says which half happened: the content is
// the new version from the moment the registry accepted it, and the way
// out is the rollback the update left behind.
func (b *App) Update(id, src string) (apps.Summary, error) {
	store, err := b.store()
	if err != nil {
		return apps.Summary{}, err
	}
	ctx := b.core.Shell.Context()
	sum, err := store.Update(ctx, id, src)
	if err != nil {
		return apps.Summary{}, err
	}
	return sum, b.applySwap(ctx, id)
}

// UpdateZip updates an application from a zip package, with the same
// entry-point rules Update has.
func (b *App) UpdateZip(id, zipPath string) (apps.Summary, error) {
	store, err := b.store()
	if err != nil {
		return apps.Summary{}, err
	}
	ctx := b.core.Shell.Context()
	sum, err := store.UpdateZip(ctx, id, zipPath)
	if err != nil {
		return apps.Summary{}, err
	}
	return sum, b.applySwap(ctx, id)
}

// Rollback puts back the version the last update replaced and serves it,
// consuming the snapshot: the application is where it was before that
// update, with its sessions and its workspace untouched.
func (b *App) Rollback(id string) (apps.Summary, error) {
	store, err := b.store()
	if err != nil {
		return apps.Summary{}, err
	}
	ctx := b.core.Shell.Context()
	sum, err := store.Rollback(ctx, id)
	if err != nil {
		return apps.Summary{}, err
	}
	return sum, b.applySwap(ctx, id)
}

// applySwap is the runtime half of a content swap: the page is told
// the application changed either way, and an application that is wanted is
// assembled on the bytes that just landed — a reload with work in flight
// retires the old generation and lets it drain, which is why an update
// never interrupts a turn. A failure is the caller's to report: the
// content is already the new version, so what the message describes is an
// application that is installed and cannot be served.
func (b *App) applySwap(ctx context.Context, id string) error {
	err := b.core.ReloadApp(ctx, id)
	b.changed(id)
	if err != nil {
		return fmt.Errorf("%q is updated but cannot be served: %w", id, err)
	}
	return nil
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
	if _, err := b.core.Runtime.EnsureHost(
		host.WithAssemblyReason(ctx, host.ReasonAppEnable),
		host.AppTarget(id),
	); err != nil {
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

// AppRecovery is one application's crash-recovery view, the numbers the
// diagnostics panel shows: the pass this process ran when it assembled
// the application, or the live process that owns the application's state
// root instead of this one.
//
// It mirrors the workspace card's RecoveryDTO with one difference that
// is the point of it: an application is named by its manifest, never by
// the state root its lock lives in, so the panel can say "this
// application is held by another live process" without leaking an
// internal path the user never chose.
type AppRecovery struct {
	// Ran reports a recovery pass this process ran for this
	// application (as opposed to one that found the root held).
	Ran bool `json:"ran"`
	// At is when that pass ran, or when another live process was found
	// holding the state root (Ran is false in that case). Empty when
	// this process has neither run a pass nor looked.
	At string `json:"at,omitempty"`
	// Recovered counts the unfinished turns the pass materialized as
	// interrupted: turns a previous process never archived.
	Recovered int `json:"recovered"`
	// Holder names the live process that owns this application's state
	// root, so this one ran no pass at all (its checkpoints are either
	// that process's live work or leftovers it deliberately left).
	// Empty means this process owns the root.
	Holder string `json:"holder,omitempty"`
}

// AppAssembly is one application's assembly record as the diagnostics
// panel renders it: how many times this process assembled the
// application's runtime, and — when an attempt was refused — exactly
// what it said, with the reason that asked for it.
//
// The error text is the point. An application that will not assemble is
// almost always a document layer the host refused, the refusal is the
// only copy of what is wrong, and the person who can fix the YAML is the
// one reading this. The counts answer the other question the same panel
// gets: how often the runtime has been rebuilt under them.
type AppAssembly struct {
	// Count is how many assemblies completed in this process (0 also
	// means "none yet", which is what LastReason distinguishes).
	Count int `json:"count"`
	// LastReason is why the most recent attempt ran — a `host.ReasonApp*`
	// name, or `unknown` for an untagged caller — and LastAt when it
	// ended, in RFC3339 UTC. Both empty when nothing has been assembled.
	LastReason string `json:"last_reason,omitempty"`
	LastAt     string `json:"last_at,omitempty"`
	// LastError is the refusal of the most recent failed attempt, with
	// the reason that asked for it and when. Empty when none failed. A
	// later success does not clear it: a page that is serving again is
	// exactly when someone wants to read what was wrong.
	LastError       string `json:"last_error,omitempty"`
	LastErrorReason string `json:"last_error_reason,omitempty"`
	LastErrorAt     string `json:"last_error_at,omitempty"`
}

// appAssembly reads the pool's record of one application's assemblies in
// the shape the panel renders. An empty pool (no runtime yet) reports
// the zero value, which the page shows as "nothing has been assembled".
func appAssembly(stats host.AssemblyStats) AppAssembly {
	out := AppAssembly{
		Count:           stats.Count,
		LastReason:      string(stats.Last.Reason),
		LastError:       stats.LastFailure.Err,
		LastErrorReason: string(stats.LastFailure.Reason),
	}
	if !stats.Last.At.IsZero() {
		out.LastAt = stats.Last.At.UTC().Format(time.RFC3339)
	}
	if !stats.LastFailure.At.IsZero() {
		out.LastErrorAt = stats.LastFailure.At.UTC().Format(time.RFC3339)
	}
	return out
}

// AppStatus is what the page's card and the diagnostics view read about
// one application's runtime: the state it is in, whether a Host serves
// it right now, the three roots involved, and what recovery did for it.
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
	// Capabilities are the host's fragments this installation opted
	// into, in manifest order: what the application may reach beyond
	// the contract layer's own surface (the tool containers, the
	// sandbox, the network gate), which is the question a user reading
	// the panel is asking. Empty is the common case.
	Capabilities []string `json:"capabilities,omitempty"`
	// Recovery is what the application's own Host reports about its
	// state root: an application's sessions live under the same kind of
	// root a workspace's do, with the same lock and the same crash pass
	// (see orchestration/host/recover.go). The zero value is "no Host,
	// nothing known" — the page then says so instead of claiming a
	// clean pass.
	Recovery AppRecovery `json:"recovery"`
	// Assembly is what this process's pool remembers about assembling
	// the application's runtime: the count, and the last refusal with
	// the reason it was asked for.
	Assembly AppAssembly `json:"assembly"`
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
		ID:           app.ID,
		Name:         app.Name,
		Enabled:      app.Enabled,
		Builtin:      app.Builtin,
		ContentRoot:  app.ContentDir,
		StateRoot:    stateRoot,
		WorkDir:      workDir,
		Capabilities: append([]string(nil), app.Capabilities...),
		Assembly: appAssembly(
			b.core.Runtime.Manager().AssemblyStats(host.AppTarget(id)),
		),
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
		out.Recovery = appRecovery(h)
	}
	return out, nil
}

// appRecovery reads one Host's crash-recovery report in the shape the
// panel renders. A Host that has not looked at its state root yet
// reports nothing, which the page shows as "no pass yet" rather than as
// a root without leftovers.
func appRecovery(h *host.Host) AppRecovery {
	report, ok := h.RecoveryReport()
	if !ok {
		return AppRecovery{}
	}
	out := AppRecovery{
		Ran:       report.WorkspaceHolder == "",
		Recovered: report.Recovered,
		Holder:    report.WorkspaceHolder,
	}
	if !report.At.IsZero() {
		out.At = report.At.UTC().Format(time.RFC3339)
	}
	return out
}

// Manifest returns one installed application's parsed manifest: the
// layers in order, the entry agent, the frontend entry and the run
// defaults. The application's own bundle reads it at scope setup (its
// defaults decide the composer's model and thinking level), and the
// page reads `defaults` for the same reason.
func (b *App) Manifest(id string) (apps.Manifest, error) {
	store, err := b.store()
	if err != nil {
		return apps.Manifest{}, err
	}
	m, err := store.Manifest(id)
	if err != nil {
		return apps.Manifest{}, err
	}
	return *m, nil
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

// ---- conversations -------------------------------------------------
//
// An application's conversations live in its own state root and are
// served by its own Host, so none of the workspace-scoped Conversation
// or Session methods can answer for them. What is the same is the
// shape of a turn: a message in, streamed deltas out, a terminal
// turn_end the transcript renders from. These methods reuse that shape
// and change the scope.

// AppTurnRequest is one turn an application page starts: the built-in
// chat surface and an application's own views send through the same
// call, so both produce the same events.
type AppTurnRequest struct {
	// ID is the application the turn belongs to.
	ID string `json:"id"`
	// ConversationID names the conversation, or is empty for a new
	// one. An application has no registry of "the current
	// conversation" the host keeps — the page holds the selection — so
	// every turn names the conversation it writes to, and an empty one
	// mints a fresh id here.
	ConversationID string          `json:"conversation_id,omitempty"`
	Message        message.Message `json:"message"`
	// AgentID names which of the application's agents runs this turn,
	// for a package that declares more than one (its manifest lists
	// them). Empty runs the entry agent. A name the application does
	// not declare is refused with the names it does.
	AgentID string `json:"agent_id,omitempty"`
	// Model and Think are the per-turn overrides the composer offers; an
	// app's page defaults them from its manifest.
	Model string `json:"model,omitempty"`
	Think string `json:"think,omitempty"`
}

// appSessionWindow is how many released store handles the page's reads
// keep alive before the pool may close the store. It matches the
// workspace listing's window: a page that lists, opens and deletes in a
// row holds no more than one at a time.
const appSessionWindow = 40

// app returns one installed application from the registry. Every method
// that names an application starts here, so an id no content root holds
// is refused by the registry's own answer rather than creating a state
// root for a typo.
func (b *App) app(id string) (apps.App, error) {
	store, err := b.store()
	if err != nil {
		return apps.App{}, err
	}
	return store.Get(id)
}

// ensureServing assembles one application's runtime when it is enabled
// and nothing is serving it, so a read that follows sees what the
// runtime would serve rather than what the store held before the crash
// pass ran.
//
// It never fails the read it precedes. A disabled application, a launch
// with no registry, or a package whose layers no longer assemble keeps
// answering reads — the store is a file and rendering history needs no
// engine (see appSessions) — and the failure is the card's business, not
// a transcript read's. A failure here is also not retried by the caller:
// the next read asks again, which is how a user who fixes the YAML sees
// it take effect on their next look.
func (b *App) ensureServing(ctx context.Context, id string) {
	app, err := b.app(id)
	if err != nil || !app.Enabled {
		// Not installed or switched off: there is no runtime to have
		// run the pass, and asking the pool would only be refused.
		return
	}
	if b.core.Runtime.HostFor(host.AppTarget(id)) != nil {
		return
	}
	// A read assembling the runtime is a distinct reason from a turn's
	// or a reload's: it is what the diagnostics panel is looking at when
	// an application that was serving a moment ago refuses to assemble
	// after an edit.
	ctx = host.WithAssemblyReason(ctx, host.ReasonAppRead)
	if _, err := b.core.Runtime.EnsureHost(ctx, host.AppTarget(id)); err != nil {
		flowtelemetry.WarnErr(ctx,
			"desktop: assembling an application for a read failed", err)
	}
}

// appSessions returns a handle on one installed application's session
// store plus the function that releases it.
//
// Always through the pool, never by borrowing the serving Host's store:
// a host that is retiring still answers HostFor, and its close would
// then shut the database under a read this method handed out. The pool
// hands back the very store the host holds — one open handle per root,
// reference-counted — so a page read keeps it alive for as long as it
// reads, whether the runtime exists, is draining, or never started.
//
// Going through the pool is also what lets the page show a conversation
// before the runtime exists (a disabled application) or after it
// retired: the store is a file, and rendering history needs no engine.
//
// One thing the store cannot answer on its own is what a crash left
// behind: the recovery pass that materializes an unfinished turn runs
// when a Host is assembled (orchestration/host/recover.go), so a read
// that overtook the pass would show the conversation with the turn
// missing — and then the same turn would appear later, once something
// else assembled the runtime. ensureServing runs first so the page's
// first read of a crashed application is already the transcript the
// runtime would serve, the way a workspace's own window open is.
func (b *App) appSessions(
	ctx context.Context, id string,
) (*sessions.Store, func(), error) {
	mgr := b.core.Runtime.Manager()
	if mgr == nil {
		return nil, nil, errNotReady("app")
	}
	b.ensureServing(ctx, id)
	layout, err := config.AppLayout(b.core.DataDir, id)
	if err != nil {
		return nil, nil, err
	}
	// An empty work dir: an application's state root never had a
	// project-local predecessor, so the opened store skips adoption
	// entirely (see host.Manager.acquireStore).
	store, err := mgr.OpenSessions(ctx, "", layout, appSessionWindow)
	if err != nil {
		return nil, nil, err
	}
	return store, func() { mgr.ReleaseSessions(store) }, nil
}

// NewSession mints the id of a new conversation for one application. The
// id is minted here rather than in a per-window registry because an
// application has no "current conversation" the host tracks: the page
// holds the selection, and every turn names the session it belongs to.
func (b *App) NewSession(id string) (string, error) {
	if _, err := b.app(id); err != nil {
		return "", err
	}
	return ids.NewSession(), nil
}

// Sessions lists one application's stored conversations, newest first.
func (b *App) Sessions(id string) ([]SessionMeta, error) {
	if _, err := b.app(id); err != nil {
		return nil, err
	}
	store, release, err := b.appSessions(b.core.Shell.Context(), id)
	if err != nil {
		return nil, err
	}
	defer release()
	return listStoredMetas(store)
}

// History returns the most recent n archived messages of one
// conversation.
func (b *App) History(
	id, conversationID string, n int,
) ([]message.Message, error) {
	if _, err := b.app(id); err != nil {
		return nil, err
	}
	store, release, err := b.appSessions(b.core.Shell.Context(), id)
	if err != nil {
		return nil, err
	}
	defer release()
	return store.History(b.core.Shell.Context(), conversationID, n)
}

// Turns returns archived turns of one conversation, oldest first. limit
// <= 0 reads every turn.
func (b *App) Turns(
	id, conversationID string, limit int, beforeSeq int64,
) ([]SessionTurnDTO, error) {
	if _, err := b.app(id); err != nil {
		return nil, err
	}
	ctx := b.core.Shell.Context()
	store, release, err := b.appSessions(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	turns, err := store.TurnsPage(ctx, conversationID, limit, beforeSeq)
	if err != nil {
		return nil, err
	}
	out := make([]SessionTurnDTO, 0, len(turns))
	for _, turn := range turns {
		out = append(out, toSessionTurnDTO(ctx, conversationID, turn))
	}
	return out, nil
}

// ActiveRun reports the run serving one conversation right now, or "".
// The page reads it after a reload or a reopen to pick up a turn that is
// still running: an application's runs outlive its page.
func (b *App) ActiveRun(id, conversationID string) string {
	h := b.core.Runtime.HostFor(host.AppTarget(id))
	if h == nil {
		return ""
	}
	for _, run := range h.ActiveRuns() {
		if run.ConversationID == conversationID {
			return run.RunID
		}
	}
	return ""
}

// StartTurn starts one turn in an installed, enabled application and
// returns immediately; the deltas and the terminal event arrive on the
// event bus, named with the application they belong to.
//
// The turn always runs in the application it names — an application owns
// its Host, its session store and its private workspace, and the window's
// active workspace has no part in it. A Host rebuild between the page's
// send and the run (an enable, an update, a settings save) is absorbed
// the way a workspace send absorbs one: wait for the replacement and
// retry inside this one call, so the page never re-sends the message.
func (b *App) StartTurn(req AppTurnRequest) (TurnStart, error) {
	app, err := b.app(req.ID)
	if err != nil {
		return TurnStart{}, err
	}
	// The registry's answer comes first: a disabled application is
	// refused here, without asking the pool to assemble anything. The
	// builder refuses it too — this is the cheap, named path to the
	// same answer, and it hands the page nothing to hold on to.
	if !app.Enabled {
		return TurnStart{}, host.ErrAppNotEnabled
	}
	ctx := b.core.Shell.Context()
	contextID := strings.TrimSpace(req.ConversationID)
	if contextID == "" {
		contextID = ids.NewSession()
	} else if !ids.IsSession(contextID) {
		return TurnStart{}, fmt.Errorf(
			"apps: invalid session id %q", contextID)
	}
	requestedAt := time.Now().UTC()
	sink := agent.StreamSinkFunc(func(
		_ context.Context,
		env event.Envelope,
		delta agent.StreamDeltaPayload,
	) error {
		if !agent.IsStreamDelta(env.Subject) {
			return nil
		}
		b.core.Shell.EmitStream(core.StreamEvent{
			AppID:          app.ID,
			RunID:          interact.StreamRunID(env.Subject),
			ConversationID: contextID,
			AgentID:        agentIDOrAssistant(env),
			ParentRunID:    env.ParentRunID(),
			Delta:          delta,
		})
		return nil
	})
	opts := host.RunOptions{
		Message:   req.Message,
		ContextID: contextID,
		AgentID:   strings.TrimSpace(req.AgentID),
		Model:     req.Model,
		Think:     req.Think,
		Backend:   b.core.Prompt,
		Sink:      sink,
		QueueSize: 256,
		// A person pressed send in the application's page, so the turn
		// may preempt a live one on the same conversation the way a
		// composer send does.
		Origin: host.OriginInteractive,
		OnUsage: func(_ context.Context, usage inference.Usage) {
			ev := core.NewUsageEvent(usage)
			ev.AppID = app.ID
			b.core.Shell.Emit(core.EventUsage, ev)
		},
	}
	var start TurnStart
	err = b.core.Runtime.Do(
		host.WithAssemblyReason(ctx, host.ReasonAppTurn),
		host.AppTarget(app.ID), nil,
		func(h *host.Host) error {
			run, err := h.StartRun(ctx, opts)
			if err != nil {
				return err
			}
			startedAt := time.Now().UTC()
			start = TurnStart{
				RunID:          run.RunID(),
				ConversationID: contextID,
				RequestedAt:    requestedAt.Format(time.RFC3339),
				StartedAt:      startedAt.Format(time.RFC3339),
			}
			// The turn's terminal event is emitted from here on its own
			// goroutine, so the RPC returns as soon as the run started. It
			// names the agent that answered — a package with several agents
			// runs one per turn — with the entry agent as the fallback for
			// a handle that never recorded one.
			agent := run.AgentID()
			if agent == "" {
				agent = app.Agent
			}
			go finishTurn(ctx, b.core, run, app.ID, agent, contextID)
			return nil
		},
	)
	if err != nil {
		return TurnStart{}, err
	}
	return start, nil
}

// Cancel stops one running turn of one application. The run is looked up
// on the application's Host — the live generation or the one retiring
// while its last runs drain — and an application with no Host has no run
// to stop.
func (b *App) Cancel(id, runID string) error {
	if _, err := b.app(id); err != nil {
		return err
	}
	h := b.core.Runtime.HostFor(host.AppTarget(id))
	if h == nil {
		return errNotReady("app")
	}
	return h.CancelRun(runID)
}

// DeleteSession removes one conversation of one application. A live run
// keeps the Host's delete path (it stops the run and waits for the
// terminal persistence before the rows go away); an application with no
// runtime deletes through the store directly, because "delete this
// conversation" must not require starting an engine.
func (b *App) DeleteSession(id, conversationID string) error {
	if _, err := b.app(id); err != nil {
		return err
	}
	if !ids.IsSession(conversationID) {
		return fmt.Errorf("apps: invalid conversation id %q", conversationID)
	}
	ctx := b.core.Shell.Context()
	if h := b.core.Runtime.HostFor(host.AppTarget(id)); h != nil {
		return h.DeleteConversation(ctx, conversationID)
	}
	store, release, err := b.appSessions(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	return store.Remove(ctx, conversationID)
}

// changed tells the page that one application was installed, enabled,
// disabled or removed, so the list and the card reload.
func (b *App) changed(id string) {
	b.core.Shell.Emit(core.EventAppChanged, core.AppChangedEvent{ID: id})
}

// appKVNamespace is the namespace one application's key/value file lives
// under inside its state root. The reused plugin store lays a namespace
// out as `.data/<namespace>/kv.json`; an application gets exactly one, so
// a write from its bundle and a read of the same key can never land in
// two places. The isolation is the state root itself: two applications
// have two roots, and `purge` on uninstall takes the file with it.
const appKVNamespace = "kv"

// kv returns the cached key/value store of one installed application.
func (b *App) kv(id string) (*plugins.KVStore, error) {
	store, err := b.store()
	if err != nil {
		return nil, err
	}
	root, err := store.StateRoot(id)
	if err != nil {
		return nil, err
	}
	b.kvMu.Lock()
	defer b.kvMu.Unlock()
	if kv, ok := b.kvStores[id]; ok {
		return kv, nil
	}
	kv := plugins.NewKVStore(root)
	b.kvStores[id] = kv
	return kv, nil
}

// KVGet returns one entry of an application's own storage. It is what the
// application's frontend bundle reads through ctx.storage, and the only
// store it can reach: the namespace is the application's state root and
// the caller never names it.
func (b *App) KVGet(id, key string) (plugins.KVEntry, error) {
	kv, err := b.kv(id)
	if err != nil {
		return plugins.KVEntry{}, err
	}
	return kv.Get(appKVNamespace, key)
}

// KVList returns every entry of an application's own storage.
func (b *App) KVList(id string) ([]plugins.KVEntry, error) {
	kv, err := b.kv(id)
	if err != nil {
		return nil, err
	}
	return kv.List(appKVNamespace)
}

// KVSet stores one entry of an application's own storage.
func (b *App) KVSet(id, key, value string) error {
	kv, err := b.kv(id)
	if err != nil {
		return err
	}
	return kv.Set(appKVNamespace, key, value)
}

// KVDelete removes one entry of an application's own storage.
func (b *App) KVDelete(id, key string) error {
	kv, err := b.kv(id)
	if err != nil {
		return err
	}
	return kv.Delete(appKVNamespace, key)
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
