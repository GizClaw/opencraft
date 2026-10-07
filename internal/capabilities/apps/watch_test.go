package apps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The watcher, driven a poll at a time: what a content root holds when
// the watcher arrives is the baseline, a change is reported once it has
// stopped moving, and the report says whether the thing that changed was
// the runtime's or the frontend bundle's.
//
// The fixture application carries a manifest, a layer, a graph, a script
// and a built bundle under ui/dist, which is the layout the two kinds
// are told apart by (see bundleOnly).

const (
	watchEntry = "export const views = [];\n"
	watchStyle = ".app { color: #000 }\n"
)

// bundleManifest renders the fixture manifest with the given layers and
// a frontend bundle whose entry and style share one directory — which is
// what makes ui/ the bundle's tree.
func bundleManifest(layers ...string) string {
	var b strings.Builder
	b.WriteString("app: v1\nid: hello\nname: Hello\nversion: 0.1.0\nlayers:\n")
	for _, layer := range layers {
		fmt.Fprintf(&b, "  - %s\n", layer)
	}
	b.WriteString("ui:\n  entry: ui/dist/index.js\n  style: ui/dist/index.css\n")
	return b.String()
}

// writeBundleApp writes the fixture application with a bundle into dir,
// with one extra layer file when the test needs one.
func writeBundleApp(t *testing.T, dir string, extraLayer string) {
	t.Helper()
	layers := []string{"layer.yaml"}
	if extraLayer != "" {
		layers = append(layers, extraLayer)
		writeTestFile(t, dir, extraLayer, "agents:\n  app:\n    card:\n      name: Extra\n")
	}
	writeTestFile(t, dir, ManifestFile, bundleManifest(layers...))
	writeTestFile(t, dir, "layer.yaml", fixtureLayer)
	writeTestFile(t, dir, "graph.yaml", fixtureGraph)
	writeTestFile(t, dir, "nodes/hello.js", fixtureScript)
	writeTestFile(t, dir, "ui/dist/index.js", watchEntry)
	writeTestFile(t, dir, "ui/dist/index.css", watchStyle)
}

// watchReports collects what a watcher reported. It is safe for the
// goroutine the Run test drives the watcher on.
type watchReports struct {
	mu   sync.Mutex
	seen []WatchChange
}

func (r *watchReports) add(ch WatchChange) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, ch)
}

// take returns what was reported since the last call, which is how every
// test below reads one step's outcome instead of replaying the sequence.
func (r *watchReports) take() []WatchChange {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.seen
	r.seen = nil
	return out
}

// newWatchStore installs the bundle fixture into a fresh registry and
// returns the store, the application's content root and the reports.
func newWatchStore(t *testing.T) (*Store, string, *watchReports) {
	t.Helper()
	store, root, _ := newStore(t)
	src := t.TempDir()
	writeBundleApp(t, src, "")
	install(t, store, src)
	return store, filepath.Join(root, "hello", "content"), &watchReports{}
}

// newTestWatcher returns a watcher over one store whose reports land in
// the recorder.
func newTestWatcher(t *testing.T, store *Store, rec *watchReports) *Watcher {
	t.Helper()
	return NewWatcher(store, WatchOptions{Report: rec.add})
}

// polls advances a watcher one scan at a time.
func polls(t *testing.T, w *Watcher, n int) {
	t.Helper()
	for range n {
		w.poll(context.Background())
	}
}

// baseline settles a watcher into the state every test below starts
// from: the tree as it is, reported as nothing.
func baseline(t *testing.T, w *Watcher, rec *watchReports) {
	t.Helper()
	polls(t, w, 2)
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("the baseline reported %v, want nothing", got)
	}
}

// only fails unless the recorder holds exactly the one expected change,
// which is what "one report per settled change" has to mean.
func only(t *testing.T, rec *watchReports, want WatchChange) {
	t.Helper()
	got := rec.take()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("reports = %v, want exactly [%+v]", got, want)
	}
}

// reportedTree returns the tree the watcher is holding for one
// application, or nil while it holds none: the state a test waits on
// before it edits.
func reportedTree(w *Watcher, id string) map[string]fileStamp {
	state := w.states[id]
	if state == nil {
		return nil
	}
	return state.settled
}

// TestWatcherFirstScanIsTheBaseline pins the rule that an installed
// application is not news: the watcher comes up next to trees that are
// already there, and nothing about them is a change.
func TestWatcherFirstScanIsTheBaseline(t *testing.T) {
	store, _, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	// A third poll, so the baseline is also a state the watcher is
	// holding still rather than one it is mid-way through.
	polls(t, w, 1)
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("a still tree reported %v, want nothing", got)
	}
}

// TestWatcherReportsARuntimeChange is the case the watcher exists for: a
// layer is edited, so the Host assembled from the old one has to go and
// the page's bundle has to reload.
func TestWatcherReportsARuntimeChange(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	writeTestFile(t, content, "layer.yaml", fixtureLayer+"\n# edited\n")
	// The scan that sees the write, then the scan that confirms it
	// stopped: one report, after the second.
	polls(t, w, 1)
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("reported %v before the tree settled", got)
	}
	polls(t, w, 1)
	only(t, rec, WatchChange{ID: "hello"})
}

// TestWatcherReportsADeletedFile is the same path read from the other
// side: a file the runtime reads is gone, which is a change whether or
// not anything was written.
func TestWatcherReportsADeletedFile(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	if err := os.Remove(filepath.Join(content, "nodes", "hello.js")); err != nil {
		t.Fatal(err)
	}
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello"})
}

// TestWatcherReportsABundleChange is the other half: the entry module is
// rewritten and the runtime is left standing, because nothing the
// runtime reads changed.
func TestWatcherReportsABundleChange(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	writeTestFile(t, content, "ui/dist/index.js", watchEntry+"// rebuilt\n")
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello", Assets: true})
}

// TestWatcherReportsABundleChangeInAChunk pins the tree rule: a file the
// manifest never named is still the bundle's when it is written where
// the entry is, which is what a bundler's chunk files are.
func TestWatcherReportsABundleChangeInAChunk(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	writeTestFile(t, content, "ui/dist/chunk-7f3a.js", "export const a = 1;\n")
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello", Assets: true})
}

// TestWatcherReportsALayerWrittenInsideTheBundleTree pins the other side
// of the same rule: what the manifest declares belongs to the runtime no
// matter where it is written, so a layer under ui/ cannot be mistaken
// for a bundle file.
func TestWatcherReportsALayerWrittenInsideTheBundleTree(t *testing.T) {
	store, root, _ := newStore(t)
	src := t.TempDir()
	writeBundleApp(t, src, "ui/extra.yaml")
	install(t, store, src)
	content := filepath.Join(root, "hello", "content")
	rec := &watchReports{}
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	writeTestFile(t, content, "ui/extra.yaml",
		"agents:\n  app:\n    card:\n      name: Extra edited\n")
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello"})
}

// TestWatcherReportsTheManifestItselfAsRuntime keeps the manifest in the
// runtime's half of the split: it names the layers, the entry agent and
// the bundle, so anything assembled from the old one is assembled from
// the wrong document.
func TestWatcherReportsTheManifestItselfAsRuntime(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	writeTestFile(t, content, ManifestFile,
		bundleManifest("layer.yaml")+"description: edited\n")
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello"})
}

// TestWatcherKeepsReportingWhileTheManifestIsBroken is the state a
// development loop spends real time in: the manifest is being edited and
// does not parse. The tree is still worth watching — the edit that fixes
// it is a change like any other — and with no ui section to read, every
// change is the runtime's.
func TestWatcherKeepsReportingWhileTheManifestIsBroken(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	writeTestFile(t, content, ManifestFile, "app: v1\nthis: [is not, a manifest")
	writeTestFile(t, content, "ui/dist/index.js", watchEntry+"// while broken\n")
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello"})
}

// TestWatcherSettlesABurstIntoOneReport is why the settle window exists:
// an editor writes a file, a bundler rewrites the directory, and the
// page hears about it once.
func TestWatcherSettlesABurstIntoOneReport(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	writeTestFile(t, content, "ui/dist/index.js", watchEntry+"// 1\n")
	polls(t, w, 1)
	writeTestFile(t, content, "ui/dist/index.js", watchEntry+"// 1 and a half\n")
	polls(t, w, 1)
	writeTestFile(t, content, "ui/dist/index.js", watchEntry+"// 2 (final)\n")
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello", Assets: true})
}

// TestWatcherReportsEverySettledEdit pins that a report moves the
// baseline: what follows is measured against the tree as last reported
// rather than against the one the watcher started with, so each kind of
// edit is classified on its own.
func TestWatcherReportsEverySettledEdit(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	writeTestFile(t, content, "ui/dist/index.js", watchEntry+"// first\n")
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello", Assets: true})
	writeTestFile(t, content, "graph.yaml", fixtureGraph+"# second\n")
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello"})
}

// TestBundleOnlyComesFromTheManifest pins the classification on its own:
// what counts as the bundle comes from the ui section and the layers the
// manifest names, never from a file extension or a directory name.
func TestBundleOnlyComesFromTheManifest(t *testing.T) {
	stamps := func(paths ...string) map[string]fileStamp {
		out := make(map[string]fileStamp, len(paths))
		for i, p := range paths {
			// Values a move cannot collide with (see moved below), or a
			// case would pass because its "change" changed nothing.
			out[p] = fileStamp{size: int64(i + 100), modTime: int64(i + 100)}
		}
		return out
	}
	// moved returns one tree with a stamp bumped on one path, which is
	// what a rewrite looks like to a scan.
	moved := func(tree map[string]fileStamp, path string) map[string]fileStamp {
		out := make(map[string]fileStamp, len(tree)+1)
		for p, stamp := range tree {
			out[p] = stamp
		}
		out[path] = fileStamp{size: 1, modTime: 1}
		return out
	}
	tree := stamps("app.yaml", "layer.yaml", "ui/dist/index.js",
		"ui/dist/index.css", "index.js")
	withUI := &Manifest{
		UI:     &UI{Entry: "ui/dist/index.js", Style: "ui/dist/index.css"},
		Layers: []string{"layer.yaml"},
	}
	cases := []struct {
		name     string
		manifest *Manifest
		path     string
		want     bool
	}{
		{"the entry module", withUI, "ui/dist/index.js", true},
		{"the stylesheet ui names", withUI, "ui/dist/index.css", true},
		{"a chunk beside the entry", withUI, "ui/dist/chunk-7f3a.js", true},
		{"a file outside the bundle tree", withUI, "index.js", false},
		{"the manifest", withUI, "app.yaml", false},
		{"a layer the manifest names", withUI, "layer.yaml", false},
		{"no manifest to read", nil, "ui/dist/index.js", false},
		{"a manifest without ui", &Manifest{Layers: []string{"layer.yaml"}},
			"ui/dist/index.js", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			after := moved(tree, tc.path)
			if got := bundleOnly(tc.manifest, tree, after); got != tc.want {
				t.Errorf("bundleOnly = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("a layer written inside the bundle tree", func(t *testing.T) {
		manifest := &Manifest{
			UI:     &UI{Entry: "ui/dist/index.js"},
			Layers: []string{"ui/extra.yaml"},
		}
		declared := stamps("ui/dist/index.js", "ui/extra.yaml")
		if bundleOnly(manifest, declared, moved(declared, "ui/extra.yaml")) {
			t.Error("a declared layer inside ui/ was read as a bundle file")
		}
	})
	t.Run("an entry at the content root names itself", func(t *testing.T) {
		// The bundle's tree is the entry's first path segment, so a
		// module at the content root has no tree: only the file itself
		// is the bundle's.
		manifest := &Manifest{UI: &UI{Entry: "index.js"}}
		atRoot := stamps("index.js", "layer.yaml")
		if !bundleOnly(manifest, atRoot, moved(atRoot, "index.js")) {
			t.Error("the entry file itself was not read as the bundle's")
		}
		if bundleOnly(manifest, atRoot, moved(atRoot, "layer.yaml")) {
			t.Error("a root-level file beside the entry was read as the bundle's")
		}
	})
	t.Run("an empty diff", func(t *testing.T) {
		if bundleOnly(withUI, tree, tree) {
			t.Error("a diff with nothing in it was read as the bundle's")
		}
	})
}

// TestWatcherIgnoresDotfiles keeps the walk aligned with the installer's
// one filter: a content root's dotfiles are not part of the application,
// and a build cache under one is exactly where the churn is.
func TestWatcherIgnoresDotfiles(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	writeTestFile(t, content, ".cache/vite/deps.js", "export const cached = 1;\n")
	writeTestFile(t, content, "ui/.swp", "editor state\n")
	polls(t, w, 2)
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("dotfile churn reported %v, want nothing", got)
	}
}

// TestWatcherWatchesOnlyEnabledApps pins the boundary: a disabled
// application has no runtime to invalidate, and enabling it assembles
// from disk as it stands, so an edit made while it is off is not a
// change the watcher has to notice.
func TestWatcherWatchesOnlyEnabledApps(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	baseline(t, w, rec)
	if err := store.SetEnabled("hello", false); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, content, "layer.yaml", fixtureLayer+"\n# edited while off\n")
	polls(t, w, 2)
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("a disabled application reported %v, want nothing", got)
	}
	// Enabling it again starts from the tree as it is: the edit made
	// while it was off is what its assembly reads, not a change.
	if err := store.SetEnabled("hello", true); err != nil {
		t.Fatal(err)
	}
	baseline(t, w, rec)
	writeTestFile(t, content, "layer.yaml",
		fixtureLayer+"\n# edited while on\n# and again\n")
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello"})
}

// TestWatcherSkipsATreeItCannotWalk pins what happens to a content root
// the walk refuses: no report — a partial tree would read as a mass
// deletion — and the skip is recorded so the log says it once rather
// than once per interval.
func TestWatcherSkipsATreeItCannotWalk(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := newTestWatcher(t, store, rec)
	w.maxFiles = 3
	polls(t, w, 2)
	if _, ok := w.skipped["hello"]; !ok {
		t.Fatal("an unwatchable tree is not recorded as skipped")
	}
	writeTestFile(t, content, "ui/dist/index.js", watchEntry+"// unwatched\n")
	polls(t, w, 2)
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("a tree past the cap reported %v, want nothing", got)
	}
	// Raised back over the tree's size, the application is watched again
	// — and its first scan is a baseline, so the bigger tree is not
	// reported as a change against the partial one left behind.
	w.maxFiles = 64
	polls(t, w, 2)
	if _, ok := w.skipped["hello"]; ok {
		t.Fatal("a watchable tree is still recorded as skipped")
	}
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("resuming a tree reported %v, want nothing", got)
	}
	writeTestFile(t, content, "ui/dist/index.js", watchEntry+"// watched again\n")
	polls(t, w, 2)
	only(t, rec, WatchChange{ID: "hello", Assets: true})
}

// TestSnapshotCountsAndSkips pins the walk itself: regular files are
// stamped by their path, dotfiles and symlinks are not files this host
// can compare, and a tree past the limit is refused whole.
func TestSnapshotCountsAndSkips(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "app.yaml", "app: v1\n")
	writeTestFile(t, dir, "ui/dist/index.js", watchEntry)
	writeTestFile(t, dir, ".hidden/state", "x\n")
	if err := os.Symlink(
		filepath.Join(dir, "ui", "dist", "index.js"),
		filepath.Join(dir, "ui", "dist", "link.js"),
	); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	tree, why := snapshot(dir, 64)
	if why != "" {
		t.Fatalf("snapshot refused the tree: %s", why)
	}
	for _, want := range []string{"app.yaml", "ui/dist/index.js"} {
		if _, ok := tree[want]; !ok {
			t.Errorf("%s is missing from the tree: %v", want, tree)
		}
	}
	for _, unwanted := range []string{".hidden/state", "ui/dist/link.js"} {
		if _, ok := tree[unwanted]; ok {
			t.Errorf("%s was stamped; the walk should not see it", unwanted)
		}
	}
	if _, why := snapshot(dir, 1); why == "" {
		t.Fatal("a tree past the limit was walked anyway")
	}
}

// TestWatcherRunReportsAndStops drives the loop the host actually
// starts: a change made while Run is polling is reported, and the
// goroutine is gone once its context is.
func TestWatcherRunReportsAndStops(t *testing.T) {
	store, content, rec := newWatchStore(t)
	w := NewWatcher(store, WatchOptions{
		Interval: 5 * time.Millisecond,
		Report:   rec.add,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	// Let the baseline land, then edit and wait for the report.
	waitFor(t, 2*time.Second, func() bool {
		return reportedTree(w, "hello") != nil
	})
	writeTestFile(t, content, "ui/dist/index.js", watchEntry+"// watched live\n")
	waitFor(t, 2*time.Second, func() bool {
		return len(rec.take()) > 0
	})
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was canceled")
	}
}

// waitFor polls a condition until it holds or the deadline passes.
func waitFor(t *testing.T, limit time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition never held")
}
