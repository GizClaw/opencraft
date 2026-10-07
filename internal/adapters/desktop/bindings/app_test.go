package bindings

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/apps"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// The application binding is the page's whole view of the platform: what
// is installed, what a package would do, the enable that proves the
// application runs, and the files it reads. These tests drive it against
// a real launch layout (one registry, one manager, a fake provider), so
// an assertion about a path or an assembly is an assertion about what a
// user would get.

// writeAppBundle writes the fixture application — the manifest, one
// layer, its graph and its script, plus the frontend bundle the page
// loads — and returns the package directory.
func writeAppBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		apps.ManifestFile: `app: v1
id: hello
name: Hello
version: 0.1.0
icon: icon.png
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
  - id: llm
    type: inference
    config:
      stream: true
edges:
  - { from: write, to: llm }
  - { from: llm, to: __end__ }
`,
		"scripts/write.js":        `fs.write("hello.txt", "written by the app\n");` + "\n",
		"icon.png":                "not really a png, but a file",
		"ui/dist/index.js":        "export function apply() {}\n",
		"ui/dist/index.css":       ".hello { color: red }\n",
		"nodes/unreachable.js":    "// a file the graph does not name\n",
		"README.md":               "the fixture application\n",
		"notes/design.md":         "# design\n",
		"notes/deeper/ignored.md": "# deeper\n",
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

// seedAppInference writes the user's inference wiring into a throwaway
// config dir, which is what an application's overlay is built from: an
// application carries no provider of its own.
func seedAppInference(t *testing.T, configDir, baseURL string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := []byte("version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(filepath.Join(configDir, "opencraft.yaml"), seed, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.InferenceConfig{Instances: []config.Instance{{
		Type:      "openai",
		Name:      "fake",
		API:       "chat",
		Endpoint:  baseURL,
		Enabled:   true,
		KeySource: config.KeyLiteral,
		KeyValue:  "test-key",
		Models:    []config.Model{{Name: "fake-model"}},
	}}}
	if err := configseed.Write(configDir, cfg); err != nil {
		t.Fatal(err)
	}
}

// appBindingFixture is one launch with the application binding over it.
type appBindingFixture struct {
	binding *App
	core    *core.Core
	// dataDir is both the state root and the app home, the single-root
	// layout the content level exists for.
	dataDir string
}

// newAppBinding builds the launch a desktop would build — one core, one
// registry over the resolved roots, everything on a throwaway tree. A
// nil provider still gets inference wiring (the endpoint is never
// contacted): the tests that do not assemble an application are about
// the registry and the reads, and "no wiring at all" is its own case
// below.
func newAppBinding(t *testing.T, provider *fakeprovider.Server) *appBindingFixture {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	endpoint := "http://127.0.0.1:1/v1"
	if provider != nil {
		endpoint = provider.URL()
	}
	seedAppInference(t, configDir, endpoint)
	c := core.NewCoreWithPaths(core.Paths{
		UserDir: configDir,
		DataDir: dataDir,
		AppHome: dataDir,
	})
	t.Cleanup(func() {
		c.Runtime.Close()
		c.Plugin.Close()
	})
	return &appBindingFixture{
		binding: NewAppBinding(c),
		core:    c,
		dataDir: dataDir,
	}
}

// uiEvent is one observed UI event: the name the frontend dispatches on
// and the payload it receives. The desktop hands every event to the
// notification sink, so a test can assert on what the page hears
// instead of on what a call returned.
type uiEvent struct {
	typ  string
	data any
}

// watch starts recording the launch's UI events and returns a reader for
// what has been seen so far. A replacement landing from the pool's
// watcher goroutine is why the recorder locks.
func (f *appBindingFixture) watch() func() []uiEvent {
	var mu sync.Mutex
	var events []uiEvent
	f.core.Shell.SetNotificationSink(func(typ string, data any) {
		mu.Lock()
		events = append(events, uiEvent{typ: typ, data: data})
		mu.Unlock()
	})
	return func() []uiEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]uiEvent(nil), events...)
	}
}

// eventNames is the sequence of names seen, for order-free membership
// and exact-sequence assertions alike.
func eventNames(events []uiEvent) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.typ)
	}
	return names
}

func TestAppListOnALaunchThatNeverInstalledOne(t *testing.T) {
	f := newAppBinding(t, nil)
	got, err := f.binding.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("applications = %+v, want none", got)
	}
}

// TestAppInspectThenInstallThenEnable walks the wizard's path end to end:
// read the package, copy it in, and prove it runs — the enable assembles
// the application for real, which is what makes "installed but broken"
// visible on the card instead of at the first message.
func TestAppInspectThenInstallThenEnable(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppBinding(t, provider)
	src := writeAppBundle(t)

	insp, err := f.binding.Inspect(src)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(insp.Refusals) != 0 {
		t.Fatalf("refusals = %v, want none", insp.Refusals)
	}
	if insp.Summary.ID != "hello" || insp.Summary.Name != "Hello" {
		t.Fatalf("inspection = %+v", insp.Summary)
	}

	sum, err := f.binding.Install(src, AppInstallOptions{})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if sum.ID != "hello" || !sum.Enabled || !sum.HasUI {
		t.Fatalf("summary = %+v", sum)
	}
	// The install copied the package and nothing else: the source is
	// still the user's directory.
	if _, err := os.Stat(filepath.Join(src, apps.ManifestFile)); err != nil {
		t.Fatalf("the source package was consumed: %v", err)
	}

	// The card's first read shows it installed, enabled and not yet
	// assembled.
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Enabled || status.Serving {
		t.Fatalf("status before enabling = %+v", status)
	}
	if want := filepath.Join(f.dataDir, "apps", "hello", "content"); status.ContentRoot != want {
		t.Fatalf("content root = %q, want %q", status.ContentRoot, want)
	}

	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	status, err = f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Serving || status.Retiring {
		t.Fatalf("status after enabling = %+v", status)
	}
	if want := filepath.Join(f.dataDir, "apps", "hello"); status.StateRoot != want {
		t.Fatalf("state root = %q, want %q", status.StateRoot, want)
	}
	if want := filepath.Join(status.StateRoot, "workspace"); status.WorkDir != want {
		t.Fatalf("work dir = %q, want %q", status.WorkDir, want)
	}
	// The enable really ran the application: its session database is
	// there, and the content root it was installed from is untouched.
	if _, err := os.Stat(filepath.Join(status.StateRoot, "sessions", "session.db")); err != nil {
		t.Fatalf("the application's session store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(status.ContentRoot, "sessions")); err == nil {
		t.Fatal("the content root grew a state directory")
	}
}

// TestAppSetEnabledRefusesAPackageThatBrokeAfterTheInstall pins the
// second preflight pass: the files may change under the registry, and an
// enable that cannot serve a turn must leave the application disabled
// with the reason handed back to the page.
func TestAppSetEnabledRefusesAPackageThatBrokeAfterTheInstall(t *testing.T) {
	f := newAppBinding(t, nil)
	sum, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	// The graph the layer names disappears after the install that
	// validated it.
	content := filepath.Join(f.dataDir, "apps", sum.ID, "content")
	if err := os.Remove(filepath.Join(content, "graph.yaml")); err != nil {
		t.Fatal(err)
	}
	err = f.binding.SetEnabled("hello", true)
	if err == nil || !strings.Contains(err.Error(), "graph.yaml") {
		t.Fatalf("enable of a broken application = %v", err)
	}
	got, err := f.binding.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Enabled {
		t.Fatalf("a failed enable left the application enabled: %+v", got)
	}
}

// TestAppEnableWithoutInferenceFailsSayingSo pins the one dependency an
// application cannot carry itself: the user's inference wiring. The
// failure is the assembly's, it names the settings page, and it leaves
// the application disabled.
func TestAppEnableWithoutInferenceFailsSayingSo(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	c := core.NewCoreWithPaths(core.Paths{
		UserDir: configDir,
		DataDir: dataDir,
		AppHome: dataDir,
	})
	t.Cleanup(func() {
		c.Runtime.Close()
		c.Plugin.Close()
	})
	b := NewAppBinding(c)
	if _, err := b.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	err := b.SetEnabled("hello", true)
	if err == nil {
		t.Fatal("an application was enabled without any inference wiring")
	}
	got, listErr := b.List()
	if listErr != nil {
		t.Fatalf("list: %v", listErr)
	}
	if len(got) != 1 || got[0].Enabled {
		t.Fatalf("a failed enable left the application enabled: %+v", got)
	}
}

// TestAppAssetServesTheContentRoot pins the page's read of its own
// bundle: the module it imports, the stylesheet it injects, and an icon,
// each as the bytes plus the media type the host read off them.
func TestAppAssetServesTheContentRoot(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	asset, err := f.binding.Asset("hello", "ui/dist/index.js")
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(asset.Data)
	if err != nil {
		t.Fatalf("asset payload: %v", err)
	}
	if string(raw) != "export function apply() {}\n" {
		t.Fatalf("asset = %q", raw)
	}
	if !strings.Contains(asset.MediaType, "javascript") {
		t.Fatalf("media type = %q", asset.MediaType)
	}
	if asset.Size != len(raw) {
		t.Fatalf("size = %d, want %d", asset.Size, len(raw))
	}
	if _, err := f.binding.Asset("hello", "../../etc/passwd"); err == nil {
		t.Fatal("an asset read out of the content root was served")
	}
	if _, err := f.binding.Asset("gone", "ui/dist/index.js"); err == nil {
		t.Fatal("an asset was served for an application that is not installed")
	}
}

// TestAppWorkspaceReadsStayInTheWorkspace pins the page's file browsing:
// one directory level at a time, text files read back, and every path
// that would leave the private workspace refused — the sessions and the
// cache beside it are the host's business.
func TestAppWorkspaceReadsStayInTheWorkspace(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(status.WorkDir, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(status.WorkDir, "notes", "note.md"), []byte("# note\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(status.WorkDir, "blob.bin"), []byte{0x00, 0x01}, 0o600,
	); err != nil {
		t.Fatal(err)
	}

	root, err := f.binding.ListFiles("hello", "")
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if len(root) != 2 || root[0].Name != "notes" || !root[0].IsDir {
		t.Fatalf("workspace listing = %+v", root)
	}
	notes, err := f.binding.ListFiles("hello", "notes")
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 || notes[0].Path != "notes/note.md" {
		t.Fatalf("notes listing = %+v", notes)
	}
	text, err := f.binding.ReadFile("hello", "notes/note.md")
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if text != "# note\n" {
		t.Fatalf("file = %q", text)
	}

	for _, tc := range []struct {
		name string
		rel  string
	}{
		{"a walk out of the workspace", "../../sessions/session.db"},
		{"an absolute path", "/etc/passwd"},
	} {
		if _, err := f.binding.ReadFile("hello", tc.rel); err == nil {
			t.Errorf("%s was read: %s", tc.name, tc.rel)
		}
		if _, err := f.binding.ListFiles("hello", tc.rel); err == nil {
			t.Errorf("%s was listed: %s", tc.name, tc.rel)
		}
	}
	if _, err := f.binding.ReadFile("hello", "blob.bin"); err == nil ||
		!strings.Contains(err.Error(), "not a text file") {
		t.Fatalf("binary read = %v", err)
	}
	if _, err := f.binding.ReadFile("hello", "notes"); err == nil ||
		!strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("directory read = %v", err)
	}
}

// TestAppUninstallKeepsTheStateUnlessItIsPurged pins the two halves of
// the removal: the content goes, the application disappears from the
// list, and the conversations and workspace files stay until the user
// asks for them to go too.
func TestAppUninstallKeepsTheStateUnlessItIsPurged(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := os.MkdirAll(status.WorkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(status.WorkDir, "kept.md"), []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.binding.Uninstall("hello", false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if got, err := f.binding.List(); err != nil || len(got) != 0 {
		t.Fatalf("list after uninstall = %+v (%v)", got, err)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "apps", "hello", "content")); err == nil {
		t.Fatal("the content root survived the uninstall")
	}
	if _, err := os.Stat(filepath.Join(status.WorkDir, "kept.md")); err != nil {
		t.Fatalf("the private workspace was removed with the content: %v", err)
	}
}

// TestAppUninstallCanTakeTheStateWithIt is the other half: "also delete
// the data" reaches the state root the content removal never touches.
func TestAppUninstallCanTakeTheStateWithIt(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := os.MkdirAll(status.WorkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(status.WorkDir, "gone.md"), []byte("gone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.binding.Uninstall("hello", true); err != nil {
		t.Fatalf("uninstall with purge: %v", err)
	}
	if _, err := os.Stat(status.StateRoot); !os.IsNotExist(err) {
		t.Fatalf("the state root survived the purge: %v", err)
	}
}

// TestAppReadsSayWhatIsNotInstalled pins the refusal a card for an
// application that was removed under the page gets: the registry's own
// answer, not an empty success.
func TestAppReadsSayWhatIsNotInstalled(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Status("gone"); err == nil {
		t.Fatal("status of an application that is not installed succeeded")
	}
	if _, err := f.binding.ListFiles("gone", ""); err == nil {
		t.Fatal("a listing of an application that is not installed succeeded")
	}
	if err := f.binding.SetEnabled("gone", true); err == nil {
		t.Fatal("enabling an application that is not installed succeeded")
	}
	if err := f.binding.Uninstall("gone", false); err == nil {
		t.Fatal("uninstalling an application that is not installed succeeded")
	}
}

// TestAppReloadRebuildsTheRuntime is the update-and-rollback path: the
// package on disk changed, the page asks for a reload, and what serves
// the application afterwards is a new generation — not the one the
// reload retired. The page hears about both halves: the registry read
// (reload the card) and the runtime that landed.
func TestAppReloadRebuildsTheRuntime(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppBinding(t, provider)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	before := f.core.Runtime.HostFor(host.AppTarget("hello"))
	if before == nil {
		t.Fatal("an enabled application has no runtime")
	}
	seen := f.watch()

	if err := f.binding.Reload("hello"); err != nil {
		t.Fatalf("reload: %v", err)
	}

	after := f.core.Runtime.HostFor(host.AppTarget("hello"))
	if after == nil {
		t.Fatal("the application has no runtime after the reload")
	}
	if after == before {
		t.Fatal("the reload kept the retired generation: the old document still serves")
	}
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Serving || status.Retiring {
		t.Fatalf("status after the reload = %+v", status)
	}
	names := eventNames(seen())
	for _, want := range []string{core.EventAppChanged, core.EventAppStatus} {
		if !slices.Contains(names, want) {
			t.Fatalf("events = %v, want %s among them", names, want)
		}
	}
	var announced bool
	for _, e := range seen() {
		if e.typ != core.EventAppStatus {
			continue
		}
		if got, ok := e.data.(core.AppStatusEvent); ok && got.ID == "hello" && got.Serving {
			announced = true
		}
	}
	if !announced {
		t.Fatalf("no serving status for the reloaded application in %+v", seen())
	}
}

// TestAppReloadRefusesAPackageThatNoLongerAssembles pins what a reload
// of a broken package does: the retired generation is gone, nothing
// replaces it, and the reason belongs to the caller (the page's card)
// rather than to a log line. The enable flag stays as the user left it —
// the reload did not decide that the application is unwanted, only that
// it cannot be served as it stands.
func TestAppReloadRefusesAPackageThatNoLongerAssembles(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppBinding(t, provider)
	sum, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	content := filepath.Join(f.dataDir, "apps", sum.ID, "content")
	if err := os.Remove(filepath.Join(content, "graph.yaml")); err != nil {
		t.Fatal(err)
	}

	err = f.binding.Reload("hello")
	if err == nil || !strings.Contains(err.Error(), "graph.yaml") {
		t.Fatalf("reload of an application that cannot assemble = %v", err)
	}
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Serving || status.Retiring {
		t.Fatalf("a failed reload left a runtime serving: %+v", status)
	}
	if !status.Enabled {
		t.Fatalf("the reload changed the registry's enablement: %+v", status)
	}
}

// TestAppReloadOfADisabledApplicationOnlyInvalidates pins the other half
// of the same decision: there is no runtime to bring back, so a reload
// succeeds and assembles nothing. An id no content root holds is still
// refused — a reload names an installed application.
func TestAppReloadOfADisabledApplicationOnlyInvalidates(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := f.binding.Reload("hello"); err != nil {
		t.Fatalf("reload of a disabled application: %v", err)
	}
	if h := f.core.Runtime.HostFor(host.AppTarget("hello")); h != nil {
		t.Fatalf("a disabled application got a runtime: %v", h)
	}
	if err := f.binding.Reload("gone"); err == nil {
		t.Fatal("reloading an application that is not installed succeeded")
	}
}

// TestAppLifecycleCallsReachThePage pins the registry half of the event
// contract: every call that changes what is installed or enabled tells
// the page, in order, naming the application it changed.
func TestAppLifecycleCallsReachThePage(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppBinding(t, provider)
	seen := f.watch()

	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	// A failed enable changed the registry too (the flag goes back off),
	// so the page has to hear about that as well; "hello" is enabled
	// here, so this one is the plain disable.
	if err := f.binding.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := f.binding.Uninstall("hello", false); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	// One per call: install, enable, disable, uninstall.
	const want = 4
	changed := 0
	for _, e := range seen() {
		if e.typ != core.EventAppChanged {
			continue
		}
		got, ok := e.data.(core.AppChangedEvent)
		if !ok || got.ID != "hello" {
			t.Fatalf("app_changed payload = %#v, want the id hello", e.data)
		}
		changed++
	}
	if changed != want {
		t.Fatalf("app_changed events = %d (%v), want %d",
			changed, eventNames(seen()), want)
	}
}

// TestAppReloadDefersUntilTheRunningTurnEnds is the desktop's half of
// the deferred swap, over the hooks the composition root installs: a
// reload during a turn cannot assemble a second Host (it would serve the
// same conversation twice), so the pool retires the old generation, the
// turn finishes on it, and the replacement is assembled when the drain
// ends — told to the page as an app status, because nothing was
// installed or enabled.
func TestAppReloadDefersUntilTheRunningTurnEnds(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppBinding(t, provider)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	before := f.core.Runtime.HostFor(host.AppTarget("hello"))
	if before == nil {
		t.Fatal("an enabled application has no runtime")
	}
	seen := f.watch()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gate := provider.HoldNext()
	t.Cleanup(gate.Release)
	run, err := before.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	select {
	case <-gate.Ready():
	case <-ctx.Done():
		t.Fatal("the turn never reached the provider")
	}

	if err := f.binding.Reload("hello"); err != nil {
		t.Fatalf("reload during a turn: %v", err)
	}
	// The running turn keeps the generation it started on: it is
	// retiring, and the pool still hands it out.
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	// Both flags are true together here, and that is the honest
	// answer: the retired generation keeps taking turns until its last
	// run ends, and a rebuild is pending behind it.
	if !status.Retiring || !status.Serving {
		t.Fatalf("status during the drain = %+v, want serving and retiring", status)
	}
	if f.core.Runtime.HostFor(host.AppTarget("hello")) != before {
		t.Fatal("the reload replaced the Host while a turn was still running on it")
	}

	gate.Release()
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("wait run: %v", err)
	}

	// The pool assembles the successor once the drain ends, and the page
	// hears that its runtime is back.
	after := waitForAppHost(t, f, before)
	if after.IsClosing() {
		t.Fatal("the successor was still retiring when it landed")
	}
	status, err = f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status after the drain: %v", err)
	}
	if !status.Serving || status.Retiring {
		t.Fatalf("status after the drain = %+v, want serving", status)
	}
	var announced bool
	for _, e := range seen() {
		if e.typ != core.EventAppStatus {
			continue
		}
		if got, ok := e.data.(core.AppStatusEvent); ok && got.ID == "hello" && got.Serving {
			announced = true
		}
	}
	if !announced {
		t.Fatalf("the page never heard the replacement land: %v", eventNames(seen()))
	}
}

// waitForAppHost waits for the application's next generation to be
// pooled. The pool assembles it on its watcher goroutine, so this is a
// poll rather than a call.
func waitForAppHost(t *testing.T, f *appBindingFixture, old *host.Host) *host.Host {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if h := f.core.Runtime.HostFor(host.AppTarget("hello")); h != nil && h != old {
			return h
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the drained application was never replaced")
	return nil
}

// TestAppManifestReadsWhatThePageAndTheBundleNeed pins the read the
// composer's defaults and the application's own bundle come from: the
// manifest as the content root holds it, and a refusal — not an empty
// manifest — for an id nothing is installed under.
func TestAppManifestReadsWhatThePageAndTheBundleNeed(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	m, err := f.binding.Manifest("hello")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.ID != "hello" || m.Name != "Hello" || m.Version != "0.1.0" {
		t.Fatalf("manifest = %+v", m)
	}
	if len(m.Layers) != 1 || m.Layers[0] != "layer.yaml" {
		t.Fatalf("layers = %v", m.Layers)
	}
	if m.UI == nil || m.UI.Entry != "ui/dist/index.js" {
		t.Fatalf("ui = %+v", m.UI)
	}
	if _, err := f.binding.Manifest("gone"); err == nil {
		t.Fatal("the manifest of an uninstalled application was returned")
	}
}

// TestAppStartTurnRunsInTheApplication is the built-in chat surface's
// path: mint a conversation, send one message, and find the turn where an
// application's turn belongs — streamed deltas and a terminal event
// named with the application, and the transcript in the application's
// own store rather than anywhere the window's workspace could see.
func TestAppStartTurnRunsInTheApplication(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hello from the app"})
	f := newAppBinding(t, provider)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	seen := f.watch()

	conversation, err := f.binding.NewSession("hello")
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if !strings.HasPrefix(conversation, "s-") {
		t.Fatalf("conversation id = %q, want an s- session", conversation)
	}
	start, err := f.binding.StartTurn(AppTurnRequest{
		ID:             "hello",
		ConversationID: conversation,
		Message:        message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	if start.RunID == "" || start.ConversationID != conversation {
		t.Fatalf("turn start = %+v", start)
	}

	end := waitForTurnEnd(t, seen, start.RunID)
	if end.AppID != "hello" {
		t.Fatalf("turn_end app_id = %q, want hello", end.AppID)
	}
	if end.AgentID != "app" {
		t.Fatalf("turn_end agent_id = %q, want the manifest's app", end.AgentID)
	}
	if end.Status != "completed" {
		t.Fatalf("turn_end = %+v", end)
	}
	// The deltas were named with the application as well: the page
	// routes on app_id, and a workspace stream with the same ids must
	// never merge into it.
	var streamed bool
	for _, e := range seen() {
		if e.typ != core.EventStream {
			continue
		}
		payload, ok := e.data.(map[string]any)
		if !ok || payload["app_id"] != "hello" || payload["conversation_id"] != conversation {
			t.Fatalf("stream payload = %#v", e.data)
		}
		streamed = true
	}
	if !streamed {
		t.Fatalf("no stream deltas for the application turn: %v", eventNames(seen()))
	}
	// Everything else the turn reports is named too: the usage stays in
	// the application's own bucket, and the turn does not touch the
	// window's busy flag — an application's spinner is the page's, and
	// clearing the composer's here would end a workspace turn's.
	var usage core.UsageEvent
	for _, e := range seen() {
		switch e.typ {
		case core.EventUsage:
			usage, _ = e.data.(core.UsageEvent)
		case core.EventStatus:
			t.Fatalf("an application turn cleared the window's status: %v", e.data)
		}
	}
	if usage.AppID != "hello" {
		t.Fatalf("usage event = %+v, want it attributed to hello", usage)
	}

	// The turn is archived in the application's own store, and the
	// page's own reads find it.
	metas, err := f.binding.Sessions("hello")
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != conversation {
		t.Fatalf("sessions = %+v", metas)
	}
	turns, err := f.binding.Turns("hello", conversation, 0, 0)
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if len(turns) != 1 || turns[0].Status != "completed" {
		t.Fatalf("turns = %+v", turns)
	}
	if got := f.binding.ActiveRun("hello", conversation); got != "" {
		t.Fatalf("active run after the turn = %q", got)
	}
	// Isolation, in the one direction a workspace-shaped bug would show:
	// the conversation lives under the application's state root, and no
	// workspace was opened or written to for it.
	st, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.StateRoot != filepath.Join(f.dataDir, "apps", "hello") {
		t.Fatalf("state root = %q", st.StateRoot)
	}
	if st.WorkDir != filepath.Join(st.StateRoot, "workspace") {
		t.Fatalf("work dir = %q", st.WorkDir)
	}
	if entries, err := os.ReadDir(filepath.Join(f.dataDir, "workspaces")); err == nil &&
		len(entries) > 0 {
		t.Fatalf("an application turn wrote to the workspace root: %v", entries)
	}
}

// TestAppTurnCanBeStopped pins the stop path the chat surface offers:
// while the provider holds the turn, the page's cancel reaches the run,
// the terminal event says so in the terms the transcript branches on —
// `canceled`, and not a timeout, which is the classification the
// frontend's isUserStop reads (store.ts: a canceled turn whose errorKind
// is not a timeout stays a user stop even when the engine reports the
// cancellation in its own words, which here is the context error) — and
// the conversation is still usable afterwards.
func TestAppTurnCanBeStopped(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "on the second try"})
	f := newAppBinding(t, provider)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	seen := f.watch()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gate := provider.HoldNext()
	t.Cleanup(gate.Release)
	conversation, err := f.binding.NewSession("hello")
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	start, err := f.binding.StartTurn(AppTurnRequest{
		ID:             "hello",
		ConversationID: conversation,
		Message:        message.NewTextMessage(message.RoleUser, "hi"),
	})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	select {
	case <-gate.Ready():
	case <-ctx.Done():
		t.Fatal("the turn never reached the provider")
	}
	if got := f.binding.ActiveRun("hello", conversation); got != start.RunID {
		t.Fatalf("active run = %q, want %q", got, start.RunID)
	}
	if err := f.binding.Cancel("hello", start.RunID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	gate.Release()

	end := waitForTurnEnd(t, seen, start.RunID)
	if end.Status != "canceled" {
		t.Fatalf("turn_end = %+v, want a canceled turn", end)
	}
	if end.ErrorKind == "timeout" {
		t.Fatalf("a stop was reported as a timeout: %+v", end)
	}

	// The stop freed the run and left the conversation usable: the page
	// can send again, and both turns are in the transcript.
	if got := f.binding.ActiveRun("hello", conversation); got != "" {
		t.Fatalf("active run after the stop = %q", got)
	}
	second, err := f.binding.StartTurn(AppTurnRequest{
		ID:             "hello",
		ConversationID: conversation,
		Message:        message.NewTextMessage(message.RoleUser, "again"),
	})
	if err != nil {
		t.Fatalf("start turn after the stop: %v", err)
	}
	if again := waitForTurnEnd(t, seen, second.RunID); again.Status != "completed" {
		t.Fatalf("second turn_end = %+v", again)
	}
	turns, err := f.binding.Turns("hello", conversation, 0, 0)
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("the stopped turn was not archived beside the second: %+v", turns)
	}
}

// TestAppReadsWorkWithoutARuntime pins what a disabled application's page
// still does: it lists, reads and deletes conversations — the store is a
// file and rendering history needs no engine — while a turn is refused
// with the registry's own answer instead of a runtime failure.
func TestAppReadsWorkWithoutARuntime(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got, err := f.binding.Sessions("hello"); err != nil || len(got) != 0 {
		t.Fatalf("sessions = %+v (%v)", got, err)
	}
	conversation, err := f.binding.NewSession("hello")
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if got, err := f.binding.Turns("hello", conversation, 0, 0); err != nil || len(got) != 0 {
		t.Fatalf("turns = %+v (%v)", got, err)
	}
	if got := f.binding.ActiveRun("hello", conversation); got != "" {
		t.Fatalf("active run of a disabled application = %q", got)
	}
	_, err = f.binding.StartTurn(AppTurnRequest{
		ID:             "hello",
		ConversationID: conversation,
		Message:        message.NewTextMessage(message.RoleUser, "hi"),
	})
	if !errors.Is(err, host.ErrAppNotEnabled) {
		t.Fatalf("start of a disabled application = %v, want ErrAppNotEnabled", err)
	}
	// A refusal hands the page nothing to hold on to: no conversation
	// id, no run id.
	if start, err := f.binding.StartTurn(AppTurnRequest{
		ID:      "hello",
		Message: message.NewTextMessage(message.RoleUser, "hi"),
	}); err == nil || start.ConversationID != "" || start.RunID != "" {
		t.Fatalf("a refused turn returned %+v (%v)", start, err)
	}
	// Deleting it does not need the runtime either.
	if err := f.binding.DeleteSession("hello", conversation); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if got, err := f.binding.Sessions("hello"); err != nil || len(got) != 0 {
		t.Fatalf("sessions after delete = %+v (%v)", got, err)
	}
	// The reads refuse an application that is not installed at all.
	if _, err := f.binding.Sessions("gone"); err == nil {
		t.Fatal("listing conversations of an uninstalled application succeeded")
	}
	if _, err := f.binding.NewSession("gone"); err == nil {
		t.Fatal("minting a conversation for an uninstalled application succeeded")
	}
}

// TestAppReadOutlivesTheHostItSharesWith pins the pooling rule the
// page's reads rest on. A read of an application's conversations takes a
// reference on the pool's store — the same handle the serving Host uses,
// one open database per root — so a Host that retires mid-read cannot
// close the database under it, and a page that reads while the runtime
// is coming or going sees a working store rather than whichever side of
// the close it landed on.
func TestAppReadOutlivesTheHostItSharesWith(t *testing.T) {
	f := newAppBinding(t, nil)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	hosted := f.core.Runtime.HostFor(host.AppTarget("hello"))
	if hosted == nil || hosted.Sessions() == nil {
		t.Fatal("enabling the application assembled no Host")
	}

	ctx := context.Background()
	store, release, err := f.binding.appSessions(ctx, "hello")
	if err != nil {
		t.Fatalf("open the page's read handle: %v", err)
	}
	defer release()
	if store != hosted.Sessions() {
		t.Fatal("the page's read opened a second database for one root")
	}

	// Disable it: the Host retires and closes its own reference, and the
	// read's handle must survive that.
	if err := f.binding.SetEnabled("hello", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	waitForRetirement(t, f, "hello")
	if store.Closed() {
		t.Fatal("the retiring Host closed the database a page read holds")
	}
	if _, err := store.List(); err != nil {
		t.Fatalf("reading through the held handle: %v", err)
	}
	release()
	// And with the handle given back, the page reads again: the store is
	// reopened from the file, runtime or no runtime.
	if got, err := f.binding.Sessions("hello"); err != nil || got == nil {
		t.Fatalf("sessions after the release = %+v (%v)", got, err)
	}
}

// waitForRetirement waits until no Host serves the application any more:
// neither the live generation nor one draining. The pool retires out of
// band (a stale Host is closed once its last run ends), so this is a poll
// on the same status the page's card shows.
func waitForRetirement(t *testing.T, f *appBindingFixture, id string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := f.binding.Status(id)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if !st.Serving && !st.Retiring {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, _ := f.binding.Status(id)
	t.Fatalf("the retired Host never left; status = %+v", st)
}

// waitForTurnEnd waits for the terminal event of one run and returns it.
// The turn ends on the Host's goroutine, so this is a poll rather than a
// call; the run id is the key rather than the conversation, because a
// conversation that was stopped and then used again has two ends.
func waitForTurnEnd(t *testing.T, seen func() []uiEvent, runID string) core.TurnEndEvent {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range seen() {
			if e.typ != core.EventTurnEnd {
				continue
			}
			end, ok := e.data.(core.TurnEndEvent)
			if ok && end.RunID == runID {
				return end
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s never ended; events = %v", runID, eventNames(seen()))
	return core.TurnEndEvent{}
}
