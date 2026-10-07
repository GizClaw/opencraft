package core

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/apps"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// The content-root watcher's desktop half: what a settled edit does to
// the pool and what the page hears about it.
//
// capabilities/apps owns the polling and the classification (see
// watch_test.go there, which drives the watcher a poll at a time). Here
// the report is handed in directly, so each test is about one decision —
// and one test at the end runs the whole thing the way the desktop does:
// a real watcher on a real tree, a real pool behind it.

// writeWatchAppPackage writes the fixture application: a manifest, one
// layer, the graph and script it names, and a frontend bundle — the
// layout the two halves of the classification are read from.
func writeWatchAppPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		apps.ManifestFile: `app: v1
id: hello
name: Hello
version: 0.1.0
agent: app
layers:
  - layer.yaml
ui:
  entry: ui/dist/index.js
  style: ui/dist/index.css
`,
		"layer.yaml": `version: v1
agents:
  app:
    card:
      name: Hello
    engine:
      settings:
        graph: { file: graph.yaml }
`,
		"graph.yaml": `name: hello
entry: write
nodes:
  - id: write
    type: script
    config:
      runtime: js
      source: { file: scripts/write.js }
edges:
  - { from: write, to: __end__ }
`,
		"scripts/write.js":  `fs.write("hello.txt", "written by the app\n");` + "\n",
		"ui/dist/index.js":  "export function apply() {}\n",
		"ui/dist/index.css": ".hello { color: red }\n",
	}
	for rel, data := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// watchCoreFixture is one launch with an installed, assembled
// application, and a recorder of what the page hears.
type watchCoreFixture struct {
	core    *Core
	content string
	events  func() []uiEvent
}

// newWatchCore builds the launch a desktop builds, installs the fixture
// application into the registry it resolves, and assembles it — so every
// test below starts where a user's would: an application with a live
// Host and a page holding its bundle.
func newWatchCore(t *testing.T) *watchCoreFixture {
	t.Helper()
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())

	c := NewCoreWithPaths(Paths{
		UserDir: configDir,
		DataDir: dataDir,
		AppHome: dataDir,
	})
	t.Cleanup(c.Runtime.Close)
	store := c.Runtime.Apps()
	if store == nil {
		t.Fatal("this launch built no application registry")
	}
	if _, err := store.Install(
		context.Background(), writeWatchAppPackage(t), apps.InstallOptions{},
	); err != nil {
		t.Fatalf("install fixture application: %v", err)
	}
	if err := c.ReloadApp(context.Background(), "hello"); err != nil {
		t.Fatalf("assemble the fixture application: %v", err)
	}
	var mu sync.Mutex
	var seen []uiEvent
	c.Shell.SetNotificationSink(func(typ string, data any) {
		mu.Lock()
		seen = append(seen, uiEvent{typ: typ, data: data})
		mu.Unlock()
	})
	return &watchCoreFixture{
		core:    c,
		content: filepath.Join(dataDir, "apps", "hello", "content"),
		events: func() []uiEvent {
			mu.Lock()
			defer mu.Unlock()
			return append([]uiEvent(nil), seen...)
		},
	}
}

// appChanges returns the app_changed payloads seen, which is the page's
// whole view of a content-root edit.
func appChanges(events []uiEvent) []AppChangedEvent {
	out := []AppChangedEvent{}
	for _, e := range events {
		if e.typ != EventAppChanged {
			continue
		}
		if data, ok := e.data.(AppChangedEvent); ok {
			out = append(out, data)
		}
	}
	return out
}

// TestBundleEditLeavesTheRuntimeStanding pins the half of the split that
// is easy to get wrong: a frontend edit reloads the page's module and
// touches nothing else. The Host serving the application is the same
// object afterwards — retiring it would kill the page's turns and
// reassemble a document that did not change.
func TestBundleEditLeavesTheRuntimeStanding(t *testing.T) {
	f := newWatchCore(t)
	target := host.AppTarget("hello")
	before := f.core.Runtime.HostFor(target)
	if before == nil {
		t.Fatal("the fixture application was not assembled")
	}
	seenBefore := len(appChanges(f.events()))

	f.core.applyAppContentChange(context.Background(), apps.WatchChange{
		ID: "hello", Assets: true,
	})

	if after := f.core.Runtime.HostFor(target); after != before {
		t.Fatal("a bundle edit replaced the application's Host")
	}
	changes := appChanges(f.events())[seenBefore:]
	if len(changes) != 1 || !changes[0].Assets || changes[0].ID != "hello" {
		t.Fatalf("the page heard %+v, want one bundle change for hello", changes)
	}
}

// TestRuntimeEditSwapsTheDocumentInPlace is the other half: a layer (or
// the manifest, or a script) changed, and the Host already serving the
// application takes the new document in place. The same Host, not
// closing — retiring it would kill the page's turns and start a second
// runtime for a document that differs in one file.
func TestRuntimeEditSwapsTheDocumentInPlace(t *testing.T) {
	f := newWatchCore(t)
	target := host.AppTarget("hello")
	before := f.core.Runtime.HostFor(target)
	if before == nil {
		t.Fatal("the fixture application was not assembled")
	}
	seenBefore := len(appChanges(f.events()))

	f.core.applyAppContentChange(context.Background(), apps.WatchChange{ID: "hello"})

	after := f.core.Runtime.HostFor(target)
	if after == nil {
		t.Fatal("no Host serves the application after the reload")
	}
	if after != before {
		t.Fatal("an edit the Host could serve in place retired it instead")
	}
	if before.IsClosing() {
		t.Error("the Host is closing after an in-place document reload")
	}
	changes := appChanges(f.events())[seenBefore:]
	if len(changes) != 1 || changes[0].Assets || changes[0].ID != "hello" {
		t.Fatalf("the page heard %+v, want one runtime change for hello", changes)
	}
}

// TestRuntimeEditOfABrokenApplicationStillReachesThePage pins what an
// author editing a layer into a document the host refuses gets: the
// reload cannot land, so no Host serves the application — and the page is
// still told, because the card and the bundle both have to re-read what
// is on disk.
func TestRuntimeEditOfABrokenApplicationStillReachesThePage(t *testing.T) {
	f := newWatchCore(t)
	if err := os.WriteFile(
		filepath.Join(f.content, "layer.yaml"),
		[]byte("version: v1\nagents:\n  app:\n    engine:\n      settings:\n        graph: { file: missing.yaml }\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	f.core.applyAppContentChange(context.Background(), apps.WatchChange{ID: "hello"})
	if h := f.core.Runtime.HostFor(host.AppTarget("hello")); h != nil {
		t.Fatalf("a document the host refuses still has a Host: %v", h)
	}
	changes := appChanges(f.events())
	if len(changes) != 1 || changes[0].Assets {
		t.Fatalf("the page heard %+v, want one runtime change for hello", changes)
	}
}

// TestRuntimeEditWithoutAHostBuildsNothing pins what an edit to an
// application nobody has open does: nothing. The watcher reports the
// change and the page re-reads its card, but no runtime is assembled for
// an application no window is showing — assembly is the first turn's or
// the page's business, and a development loop that edited a package ten
// times would otherwise build ten generations of a runtime nobody used.
func TestRuntimeEditWithoutAHostBuildsNothing(t *testing.T) {
	f := newWatchCore(t)
	target := host.AppTarget("hello")
	if err := f.core.Runtime.ReloadApps(context.Background(), "hello"); err != nil {
		t.Fatalf("retire the fixture application: %v", err)
	}
	if h := f.core.Runtime.HostFor(target); h != nil {
		t.Fatalf("the fixture application is still assembled: %v", h)
	}

	f.core.applyAppContentChange(context.Background(), apps.WatchChange{ID: "hello"})

	if h := f.core.Runtime.HostFor(target); h != nil {
		t.Fatalf("an edit assembled a runtime no page was showing: %v", h)
	}
	if changes := appChanges(f.events()); len(changes) != 1 || changes[0].Assets {
		t.Fatalf("the page heard %+v, want one runtime change for hello", changes)
	}
}

// TestStartAndStopAppWatch drives the wiring the desktop actually runs:
// the watcher reads the registry's content roots, an edit settles into a
// document reload, and StopAppWatch returns instead of leaving a
// goroutine behind. It is the one test here that waits on real ticks.
func TestStartAndStopAppWatch(t *testing.T) {
	f := newWatchCore(t)
	target := host.AppTarget("hello")
	before := f.core.Runtime.HostFor(target)
	if before == nil {
		t.Fatal("the fixture application was not assembled")
	}
	f.core.StartAppWatch()
	// A second start is a no-op rather than a second poller: the two
	// would report every edit twice.
	f.core.StartAppWatch()
	t.Cleanup(f.core.StopAppWatch)

	if err := os.WriteFile(
		filepath.Join(f.content, "layer.yaml"),
		[]byte("version: v1\nagents:\n  app:\n    card:\n      name: Edited\n    engine:\n      settings:\n        graph: { file: graph.yaml }\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	// The edit has to hold still for one interval and then be reported
	// on the next: two ticks, plus the reload itself.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := appChanges(f.events()); len(got) > 0 && !got[len(got)-1].Assets {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := appChanges(f.events()); len(got) == 0 || got[len(got)-1].Assets {
		t.Fatalf("the watcher never reported the edited application: %+v", got)
	}
	// The edit landed on the Host that was already serving it: the
	// watcher did not pay for a second assembly of one changed file.
	if after := f.core.Runtime.HostFor(target); after != before {
		t.Fatalf("the watcher replaced the Host instead of reloading its document: %v", after)
	}
	f.core.StopAppWatch()
	// The stop waited for the poll in flight: nothing is left to report
	// into a pool that is closing.
	if f.core.appWatchRun != nil {
		t.Fatal("StopAppWatch left the watcher registered")
	}
}

// TestStartAppWatchWithoutARegistry pins the launch that has no
// application root at all — a runtime built without an app home — where
// there are no content roots to watch and nothing may be started.
func TestStartAppWatchWithoutARegistry(t *testing.T) {
	dataDir := t.TempDir()
	c := &Core{Runtime: NewRuntime(dataDir, dataDir, "")}
	t.Cleanup(c.Runtime.Close)
	if c.Runtime.Apps() != nil {
		t.Fatal("a runtime built without an app home has a registry")
	}
	c.StartAppWatch()
	if c.appWatchRun != nil {
		t.Fatal("a watcher was started for a registry that does not exist")
	}
	// Stopping one that never started is a no-op rather than a panic:
	// the desktop calls it on every shutdown.
	c.StopAppWatch()
}
