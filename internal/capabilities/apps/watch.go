package apps

// The development loop's automatic half: polling the content roots of
// installed applications so an edit reaches the runtime — and the page's
// bundle — without anyone pressing a button.
//
// A content root is the one tree an application's author edits: the
// manifest, the deployment layers, the graphs and scripts the layers
// reference, the frontend bundle. The registry keeps no cache of it, and
// "open the directory, edit, reassemble" is the loop the whole layout is
// built around (doc.go). This file adds the noticing.
//
// One report per settled change. Settled, because an editor and a
// bundler write in bursts: a change has to survive a whole interval
// unchanged before it is reported, so the page loads the module a build
// finished writing instead of whichever half-written file a scan
// happened to catch.
//
// What a report carries is the one distinction the host acts on
// differently. A change inside the frontend bundle leaves the runtime
// alone — the page loads the module again and the pooled Host stands —
// while anything else (the manifest, a layer, a file a layer reads) is
// the runtime's input, so the Host assembled from the old one has to go.
// The bundle's tree comes from the manifest's own ui section, never from
// a file extension: see bundleOnly.
//
// Nothing here writes to a content root, and a tree the watcher cannot
// walk is skipped with one log line rather than reported as churn.

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

// DefaultWatchInterval is how often the content roots of enabled
// applications are walked. It is also the settle window, so the delay
// between an edit and its report is one to two intervals.
const DefaultWatchInterval = time.Second

// maxWatchFiles bounds one application's walk. A content root is a
// manifest, a handful of layers and a built bundle — hundreds of files.
// A tree past this is not that (an author's node_modules, a checkout
// copied in by mistake), and walking it once a second to find out is not
// a cost the host pays quietly: the application is skipped, with the
// tree and the limit named in the log.
const maxWatchFiles = 20_000

// WatchChange is one settled change under one installed application's
// content root.
type WatchChange struct {
	// ID is the installed application the change is under.
	ID string
	// Assets reports that every changed path belongs to the
	// application's frontend bundle. The runtime reads none of those
	// files, so nothing has to be reassembled; what has to happen is
	// the page loading the module again.
	Assets bool
}

// WatchOptions configures a Watcher.
type WatchOptions struct {
	// Interval is the polling period, and the settle window a change
	// has to outlast. Zero means DefaultWatchInterval.
	Interval time.Duration
	// Report is called once per settled change, from the goroutine
	// running the watcher. A watcher without one does nothing.
	Report func(WatchChange)
}

// Watcher polls the content roots of the registry's enabled
// applications. It keeps one snapshot per application between polls, so
// a poll reads what changed and never the whole tree twice.
type Watcher struct {
	store    *Store
	interval time.Duration
	report   func(WatchChange)
	// maxFiles is the walk's file cap (maxWatchFiles). It is a field so
	// a test can watch a tree it can also count.
	maxFiles int
	// states is one entry per application the last poll could watch.
	states map[string]*watchState
	// skipped remembers why an application was not watched, so an
	// unwatchable tree is one log line per application rather than one
	// per interval. The reason is the key: a tree that becomes
	// watchable again and then unwatchable for another reason says so.
	skipped map[string]string
	// baselined is true once a first scan has been taken, whether by
	// Baseline or by Run's own opening scan.
	baselined bool
}

// watchState is what one application's last two scans saw.
type watchState struct {
	// settled is the tree as last reported. Every change is measured
	// against it, so a report carries the paths that moved since the
	// last one — and only those, which is what keeps a bundle edit
	// following a layer edit from being read as a layer edit.
	settled map[string]fileStamp
	// observed is the tree as of the previous scan.
	observed map[string]fileStamp
	// dirty is true while observed differs from settled: the change is
	// real, and has not yet held still for a whole interval.
	dirty bool
}

// fileStamp is what a scan remembers about one file: enough to tell a
// rewritten file from an untouched one without reading it. Size and
// modification time are the pair every filesystem this host runs on
// keeps at a resolution finer than the polling interval.
type fileStamp struct {
	size    int64
	modTime int64 // UnixNano
}

// NewWatcher returns a watcher over one registry. The store is read
// through its public surface, so a watcher running beside an install, an
// update or an uninstall sees each of them between polls.
func NewWatcher(store *Store, opts WatchOptions) *Watcher {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	return &Watcher{
		store:    store,
		interval: interval,
		report:   opts.Report,
		maxFiles: maxWatchFiles,
		states:   make(map[string]*watchState),
		skipped:  make(map[string]string),
	}
}

// Run polls until ctx is done. The first poll is the baseline: what a
// content root holds when the watcher starts is not a change, so a
// watcher that comes up next to an installed application reports the
// first edit rather than the install. Run takes that scan itself unless
// Baseline already took it (see there).
func (w *Watcher) Run(ctx context.Context) {
	if w.report == nil || w.store == nil {
		return
	}
	if !w.baselined {
		w.poll(ctx)
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

// Baseline takes the first scan now, on the calling goroutine, instead
// of leaving it to Run's opening poll. What it buys a caller is the
// guarantee that the tree is held the moment it returns: a caller that
// starts polling in the background and then lets a user edit has an
// edit, not a baseline, in the unlikely window before Run is scheduled.
func (w *Watcher) Baseline(ctx context.Context) {
	if w.report == nil || w.store == nil || w.baselined {
		return
	}
	w.poll(ctx)
}

// poll walks every application that wants a runtime.
func (w *Watcher) poll(ctx context.Context) {
	w.baselined = true
	summaries, err := w.store.List()
	if err != nil {
		telemetry.WarnErr(ctx, "apps: watch: listing applications failed", err)
		return
	}
	live := make(map[string]bool, len(summaries))
	for _, sum := range summaries {
		switch {
		case !sum.Enabled:
			// A disabled application has nothing to invalidate: enabling
			// it validates and assembles from disk as it stands, so an
			// edit made while it is off is picked up by definition.
		case sum.Builtin:
			// A built-in's content root is the read-only bundle beside
			// the executable. Nobody edits it at runtime, and a packaged
			// bundle is the one tree here that may legitimately be big.
		default:
			live[sum.ID] = true
			w.pollApp(ctx, sum.ID)
		}
	}
	// Everything else starts over: an application that is gone, disabled
	// or was newly enabled gets its next poll as a baseline, so an edit
	// made while it was not watched is never reported as news.
	forget(w.states, live)
	forget(w.skipped, live)
}

// forget drops the map entries of applications that are no longer live.
func forget[V any](m map[string]V, live map[string]bool) {
	for id := range m {
		if !live[id] {
			delete(m, id)
		}
	}
}

// pollApp walks one application and reports its change once the tree has
// held still.
func (w *Watcher) pollApp(ctx context.Context, id string) {
	root, err := w.store.ContentRoot(id)
	if err != nil {
		// Uninstalled between the listing and now. The next poll's list
		// is the authority; nothing to say about a tree that is gone.
		delete(w.states, id)
		return
	}
	tree, why := snapshot(root, w.maxFiles)
	if why != "" {
		w.skip(ctx, id, why)
		return
	}
	delete(w.skipped, id)
	state := w.states[id]
	if state == nil {
		w.states[id] = &watchState{settled: tree, observed: tree}
		return
	}
	if sameTree(state.observed, tree) {
		if state.dirty {
			w.report(w.change(id, state.settled, tree))
			state.settled = tree
			state.dirty = false
		}
		return
	}
	// The tree moved again, so whatever was pending is not settled and
	// the change to report is the diff against the last reported tree.
	state.observed = tree
	state.dirty = !sameTree(state.settled, tree)
}

// skip records that one application cannot be watched, and says so once
// per application per reason.
func (w *Watcher) skip(ctx context.Context, id, why string) {
	if w.skipped[id] == why {
		return
	}
	w.skipped[id] = why
	delete(w.states, id)
	telemetry.Warn(ctx, fmt.Sprintf(
		"apps: application %q is not being watched: %s", id, why))
}

// change builds the report for one settled diff. The kind of change
// comes from the manifest's ui section, read here rather than on every
// poll: a manifest that cannot be read has no bundle to spare, so every
// change under it is the runtime's.
func (w *Watcher) change(
	id string,
	before, after map[string]fileStamp,
) WatchChange {
	var manifest *Manifest
	if m, err := w.store.Manifest(id); err == nil {
		manifest = m
	}
	return WatchChange{ID: id, Assets: bundleOnly(manifest, before, after)}
}

// bundleOnly reports whether every path that differs between two trees
// belongs to the frontend bundle a manifest declares.
//
// The bundle's tree is the module's first path segment under the content
// root: ui/dist/index.js puts the whole of ui/ in the bundle, which is
// what a built bundle needs — it is a directory of files a bundler
// rewrites together. The manifest and the layers it names are never part
// of that tree wherever they sit, so a layer written under ui/ is still
// the runtime's input.
//
// A manifest that declares no ui has no bundle at all: every change
// under it is the runtime's.
func bundleOnly(
	manifest *Manifest,
	before, after map[string]fileStamp,
) bool {
	if manifest == nil || manifest.UI == nil {
		return false
	}
	entry := slashPath(manifest.UI.Entry)
	if entry == "" {
		return false
	}
	files := map[string]bool{entry: true}
	if style := slashPath(manifest.UI.Style); style != "" {
		files[style] = true
	}
	tree := ""
	if first, _, found := strings.Cut(entry, "/"); found {
		tree = first
	}
	reserved := map[string]bool{ManifestFile: true}
	for _, layer := range manifest.Layers {
		reserved[slashPath(layer)] = true
	}
	changed := false
	for path, stamp := range after {
		if prev, ok := before[path]; ok && prev == stamp {
			continue
		}
		changed = true
		if !inBundle(tree, files, reserved, path) {
			return false
		}
	}
	for path := range before {
		if _, ok := after[path]; ok {
			continue
		}
		changed = true
		if !inBundle(tree, files, reserved, path) {
			return false
		}
	}
	// A diff with nothing in it is not a change, and the safe answer for
	// one is the runtime's.
	return changed
}

// inBundle reports whether one content-root-relative path belongs to the
// bundle: one of the files ui names, or anywhere under the bundle's
// tree. A reserved path — the manifest, a declared layer — belongs to
// the runtime no matter where it is written.
func inBundle(tree string, files, reserved map[string]bool, path string) bool {
	if reserved[path] {
		return false
	}
	if files[path] {
		return true
	}
	return tree != "" && strings.HasPrefix(path, tree+"/")
}

// slashPath puts one manifest path into the slash-spelled, content-root-
// relative form every snapshot key uses. An empty or self-referential
// path is "", which callers read as "the manifest named nothing".
func slashPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." {
		return ""
	}
	return p
}

// snapshot walks one content root and returns a stamp per regular file,
// keyed by slash-spelled path relative to the root, up to limit files.
// why is non-empty when the tree cannot be watched, and then the map is
// unusable: a partial tree would make the next scan look like a mass
// deletion, which is the one diff nothing should act on.
//
// Dot-prefixed entries are skipped, the rule the installer copies by: a
// content root's dotfiles are not part of the application (.git, an
// editor's state, a build cache) and they are where the churn is.
func snapshot(root string, limit int) (map[string]fileStamp, string) {
	files := make(map[string]fileStamp, 64)
	why := ""
	err := filepath.WalkDir(root, func(
		path string,
		d fs.DirEntry,
		err error,
	) error {
		if err != nil {
			if d != nil && d.IsDir() {
				// One unreadable directory. The rest of the tree is
				// still worth comparing, and the next scan tries again.
				return fs.SkipDir
			}
			// A file that vanished between the read and the stat is not
			// a scan failure: the next scan sees the tree it left.
			return nil
		}
		if path == root {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			// Symlinks and devices are not content: the installer
			// refuses to copy one, so a tree holding one is not a tree
			// this watcher can account for.
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if len(files) >= limit {
			why = fmt.Sprintf("the tree holds more than %d files", limit)
			return fs.SkipAll
		}
		files[filepath.ToSlash(rel)] = fileStamp{
			size:    info.Size(),
			modTime: info.ModTime().UnixNano(),
		}
		return nil
	})
	if why != "" {
		return nil, why
	}
	if err != nil {
		return nil, fmt.Sprintf("reading %s failed: %v", root, err)
	}
	return files, ""
}

// sameTree reports whether two scans saw the same files with the same
// stamps.
func sameTree(a, b map[string]fileStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for path, stamp := range a {
		if other, ok := b[path]; !ok || other != stamp {
			return false
		}
	}
	return true
}
