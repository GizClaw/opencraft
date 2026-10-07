package bindings

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
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

// TestAppStatusCarriesTheAssemblyRecord pins the two halves of the
// panel's assembly row: the pool's counting and naming, and the wire
// shape the row reads. The count is what turns "this application is
// slow" into "it was rebuilt eleven times since you opened it", the
// reason is which caller did it, and the refusal is the text a user
// with the YAML open needs — so a rename of any of these fields would
// silently blank the one copy of a broken package's error.
func TestAppStatusCarriesTheAssemblyRecord(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppBinding(t, provider)
	pkg := writeAppBundle(t)
	if _, err := f.binding.Install(pkg, AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Nothing has assembled it yet: the page says so instead of
	// claiming a runtime that was never built, and the empty fields
	// stay off the wire (the row tests them for content, not presence).
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Assembly != (AppAssembly{}) {
		t.Fatalf("an application nothing assembled = %+v", status.Assembly)
	}
	if raw, err := json.Marshal(status); err != nil {
		t.Fatalf("marshal: %v", err)
	} else {
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		assembly, ok := wire["assembly"]
		if !ok {
			t.Fatalf("the status carries no assembly: %s", raw)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(assembly, &fields); err != nil {
			t.Fatalf("unmarshal assembly: %v", err)
		}
		if _, ok := fields["count"]; !ok {
			t.Fatalf("the assembly record has no count: %s", assembly)
		}
		for _, key := range []string{"last_reason", "last_at"} {
			if _, ok := fields[key]; ok {
				t.Fatalf("an unattempted assembly carries %s: %s", key, assembly)
			}
		}
	}

	// The enable is the first assembly, and the record names it as
	// such: the page's "why did this happen" answer.
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	status, err = f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status after the enable: %v", err)
	}
	if status.Assembly.Count != 1 || status.Assembly.LastReason != "app_enable" {
		t.Fatalf("after the enable: %+v", status.Assembly)
	}
	if _, err := time.Parse(time.RFC3339, status.Assembly.LastAt); err != nil {
		t.Fatalf("the assembly moment is not a time the page can render: %q",
			status.Assembly.LastAt)
	}
	if status.Assembly.LastError != "" || status.Assembly.LastErrorReason != "" {
		t.Fatalf("a clean enable recorded a refusal: %+v", status.Assembly)
	}

	// A read that finds no runtime assembles one, and says that is what
	// asked: the panel distinguishes a page read from a turn.
	f.core.Runtime.Manager().InvalidateApps(context.Background(), "hello")
	if _, err := f.binding.Sessions("hello"); err != nil {
		t.Fatalf("sessions: %v", err)
	}
	status, err = f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status after the read: %v", err)
	}
	if status.Assembly.Count != 2 || status.Assembly.LastReason != "app_read" {
		t.Fatalf("after the read's assembly: %+v", status.Assembly)
	}

	// The breakage an author hits: a file the layer points at is gone,
	// so the reload refuses. The refusal is now the record's last
	// attempt — with the reason that asked for it — while the count
	// stays where it was: a package that will not assemble is not a
	// runtime the page can talk to.
	content := filepath.Join(f.dataDir, "apps", "hello", "content")
	if err := os.Remove(filepath.Join(content, "graph.yaml")); err != nil {
		t.Fatal(err)
	}
	err = f.binding.Reload("hello")
	if err == nil || !strings.Contains(err.Error(), "graph.yaml") {
		t.Fatalf("reload of a broken application = %v", err)
	}
	status, err = f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status after the refusal: %v", err)
	}
	if status.Assembly.Count != 2 {
		t.Fatalf("a refusal counted as an assembly: %+v", status.Assembly)
	}
	if status.Assembly.LastReason != "app_reload" ||
		status.Assembly.LastErrorReason != "app_reload" {
		t.Fatalf("the refusal does not name what asked for it: %+v",
			status.Assembly)
	}
	if !strings.Contains(status.Assembly.LastError, "graph.yaml") {
		t.Fatalf("the refusal does not carry the assembly's own words: %+v",
			status.Assembly)
	}
	if _, err := time.Parse(time.RFC3339, status.Assembly.LastErrorAt); err != nil {
		t.Fatalf("the refusal has no renderable moment: %q",
			status.Assembly.LastErrorAt)
	}
	if raw, err := json.Marshal(status); err != nil {
		t.Fatalf("marshal: %v", err)
	} else {
		for _, key := range []string{
			"count", "last_reason", "last_at",
			"last_error", "last_error_reason", "last_error_at",
		} {
			if !strings.Contains(string(raw), `"`+key+`"`) {
				t.Fatalf("the refused status carries no %s: %s", key, raw)
			}
		}
	}

	// Fixing the file brings the application back, and the refusal
	// stays readable: the page that just started serving again is
	// exactly where someone looks to find out what was wrong.
	original, err := os.ReadFile(filepath.Join(pkg, "graph.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "graph.yaml"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.binding.Reload("hello"); err != nil {
		t.Fatalf("reload after the repair: %v", err)
	}
	status, err = f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status after the repair: %v", err)
	}
	if status.Assembly.Count != 3 || status.Assembly.LastReason != "app_reload" {
		t.Fatalf("after the repair: %+v", status.Assembly)
	}
	if !strings.Contains(status.Assembly.LastError, "graph.yaml") {
		t.Fatalf("the repair cleared what was wrong: %+v", status.Assembly)
	}
	if status.Assembly.LastErrorReason != "app_reload" {
		t.Fatalf("the standing refusal lost the reason that asked for it: %+v",
			status.Assembly)
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
// writeAppBundleWithAgents rewrites the fixture package into one that
// plays two roles: the manifest lists a judge, and the layer declares it
// with the contract's committer. The entry agent's own layer is
// untouched, because that is the shape a real package has — the second
// agent is a second block, not a second application.
func writeAppBundleWithAgents(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		apps.ManifestFile: `app: v1
id: hello
name: Hello
version: 0.1.0
agent: app
agents:
  - judge
layers:
  - layer.yaml
`,
		"layer.yaml": `version: v1
agents:
  app:
    card:
      name: Hello
    engine:
      settings:
        graph: { file: graph.yaml }
  judge:
    card:
      name: Judge
      description: the second agent
    engine:
      kind: agent.Engine
      impl: graph
      deps:
        inference: infer
        router: router
        workspace: ws
        script_runtime: js
      settings:
        graph: { file: judge.yaml }
    commit:
      - type: opencraft.commit
        deps:
          memory: mem
          sessions: sessions
`,
		"judge.yaml": `name: judge
entry: verdict
nodes:
  - id: verdict
    type: script
    config:
      runtime: js
      source: { file: scripts/judge.js }
  - id: llm
    type: inference
    config:
      stream: true
edges:
  - { from: verdict, to: llm }
  - { from: llm, to: __end__ }
`,
		"scripts/judge.js": `fs.write("verdict.txt", "judged\n");` + "\n",
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
}

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

// TestAppStartTurnRunsTheAgentItNames: a package that plays two roles is
// run one agent per turn, and the request is where the choice lives. The
// turn has to be that agent's — its own graph, and a terminal event that
// names it — while the conversation stays the one the caller named,
// because whose turn it was is not which history it joined.
func TestAppStartTurnRunsTheAgentItNames(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "the judge speaks"})
	f := newAppBinding(t, provider)
	pkg := writeAppBundle(t)
	writeAppBundleWithAgents(t, pkg)
	if _, err := f.binding.Install(pkg, AppInstallOptions{}); err != nil {
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
	start, err := f.binding.StartTurn(AppTurnRequest{
		ID:             "hello",
		ConversationID: conversation,
		AgentID:        "judge",
		Message:        message.NewTextMessage(message.RoleUser, "who wins?"),
	})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	if start.ConversationID != conversation {
		t.Fatalf("the named agent answered in another conversation: %+v", start)
	}

	end := waitForTurnEnd(t, seen, start.RunID)
	if end.AgentID != "judge" {
		t.Fatalf("turn_end agent_id = %q, want the agent the turn named", end.AgentID)
	}
	if status := end.Status; status != "completed" {
		t.Fatalf("turn_end = %+v", end)
	}
	// The judge's graph ran: its script wrote its file into the
	// application's private workspace, and the entry agent's graph — which
	// writes hello.txt on every turn — did not.
	status, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if _, err := os.Stat(filepath.Join(status.WorkDir, "verdict.txt")); err != nil {
		t.Fatalf("the named agent's graph did not run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(status.WorkDir, "hello.txt")); err == nil {
		t.Fatalf("a turn naming the judge ran the entry agent's graph")
	}
	// One conversation, whichever agent answered: the page reloads its
	// turns from the store, so the judge's turn has to be there.
	turns, err := f.binding.Turns("hello", conversation, 0, 0)
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if len(turns) != 1 || turns[0].Status != "completed" {
		t.Fatalf("turns = %+v", turns)
	}
}

// TestAppStartTurnRefusesAnAgentThePackageDoesNotHave: the names are the
// package's, so a page asking for one the manifest does not list is
// asking for something that does not exist. The refusal names the agents
// it does have, which is what the page needs to correct itself.
func TestAppStartTurnRefusesAnAgentThePackageDoesNotHave(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	f := newAppBinding(t, provider)
	pkg := writeAppBundle(t)
	writeAppBundleWithAgents(t, pkg)
	if _, err := f.binding.Install(pkg, AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	_, err := f.binding.StartTurn(AppTurnRequest{
		ID:      "hello",
		AgentID: "referee",
		Message: message.NewTextMessage(message.RoleUser, "who wins?"),
	})
	if err == nil {
		t.Fatal("a turn naming an agent the package does not declare was accepted")
	}
	if !strings.Contains(err.Error(), `agent "referee"`) ||
		!strings.Contains(err.Error(), "(app, judge)") {
		t.Errorf("refusal %q does not name the agents the package has", err)
	}
	// The refusal came before anything was done for the turn: a
	// conversation minted for a name that does not exist would be a row
	// the page can open and no turn can ever fill.
	metas, err := f.binding.Sessions("hello")
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("a refused turn left sessions behind: %+v", metas)
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

// crashCheckpointAt is the checkpoint a process killed mid-turn leaves
// behind: the board the run had so far, the request that started it, and
// a timestamp older than this process — which is what tells the pass
// this turn is a leftover rather than a sibling's live work.
func crashCheckpointAt(t *testing.T, conversationID, runID string) agent.Checkpoint {
	t.Helper()
	request := agent.Request{
		ContextID: conversationID,
		Message: message.NewTextMessage(
			message.RoleUser, "the turn the crash ate"),
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	board := agent.NewBoard()
	board.SetVar("oc_thread_id", "oc-"+conversationID)
	board.AppendChannelMessage(agent.MainChannel, message.NewTextMessage(
		message.RoleUser, "the turn the crash ate"))
	board.AppendChannelMessage(agent.MainChannel, message.NewTextMessage(
		message.RoleAssistant, "partial answer before the crash"))
	return agent.Checkpoint{
		ExecID:            runID,
		Steps:             []string{"world"},
		Iteration:         1,
		Board:             board.Snapshot(),
		Timestamp:         time.Now().Add(-time.Minute),
		OriginalStartedAt: time.Now().Add(-2 * time.Minute),
		Attributes: map[string]string{
			"oc.conversation_id": conversationID,
			"oc.request":         string(raw),
		},
	}
}

// TestAppReadsRecoverBeforeTheyAnswer pins what an application's page
// sees after a crash: the pass that materializes an unfinished turn runs
// at assembly, so the page's own read of the transcript is what has to
// assemble the runtime. A read that overtook the pass would show the
// conversation with the turn missing and then grow the turn later, once
// something else happened to assemble — the same transcript, two
// answers.
func TestAppReadsRecoverBeforeTheyAnswer(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
	f := newAppBinding(t, provider)
	if _, err := f.binding.Install(writeAppBundle(t), AppInstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.binding.SetEnabled("hello", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	seen := f.watch()
	startAppTurn(t, f, seen, "hello", "", "first")
	conversations, err := f.binding.Sessions("hello")
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(conversations) != 1 {
		t.Fatalf("sessions = %+v, want the one the turn created", conversations)
	}
	conversation := conversations[0].ID

	// The crash: a checkpoint with no archive row, and then the process
	// that owned the runtime is gone.
	h := f.core.Runtime.HostFor(host.AppTarget("hello"))
	if h == nil {
		t.Fatal("no Host serves the application")
	}
	cp := crashCheckpointAt(t, conversation, "run-appcrash0001")
	if err := h.Sessions().State().Save(context.Background(), cp); err != nil {
		t.Fatalf("save crash checkpoint: %v", err)
	}
	f.core.Runtime.Close()

	// A fresh launch: the page reads the application's conversations and
	// its transcript before it does anything else with it.
	c := core.NewCoreWithPaths(core.Paths{
		UserDir: filepath.Join(f.dataDir, "config"),
		DataDir: f.dataDir,
		AppHome: f.dataDir,
	})
	t.Cleanup(func() {
		c.Runtime.Close()
		c.Plugin.Close()
	})
	b := NewAppBinding(c)
	if _, err := b.Sessions("hello"); err != nil {
		t.Fatalf("sessions after the crash: %v", err)
	}
	turns, err := b.Turns("hello", conversation, 0, 0)
	if err != nil {
		t.Fatalf("turns after the crash: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want the completed one and the recovered one",
			len(turns))
	}
	recovered := turns[len(turns)-1]
	if recovered.RunID != cp.ExecID || recovered.Status != "interrupted" {
		t.Fatalf("last turn = %+v, want interrupted run %s",
			recovered, cp.ExecID)
	}
	// The same read is where the page asks how the crash went: the
	// status now reports the pass that just ran, with the count.
	status, err := b.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Recovery.Ran || status.Recovery.Recovered != 1 {
		t.Fatalf("status recovery = %+v, want the one recovered turn",
			status.Recovery)
	}
	if status.Recovery.At == "" {
		t.Fatalf("status recovery names no moment: %+v", status.Recovery)
	}
}

// startAppTurn sends one message to an application and waits for the
// turn to end. What an application's turn answers is read off the
// provider that served it (Calls), so a test can tell which deployment
// the turn ran on.
func startAppTurn(
	t *testing.T,
	f *appBindingFixture,
	seen func() []uiEvent,
	id, conversation, text string,
) {
	t.Helper()
	start, err := f.binding.StartTurn(AppTurnRequest{
		ID:             id,
		ConversationID: conversation,
		Message:        message.NewTextMessage(message.RoleUser, text),
	})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	if end := waitForTurnEnd(t, seen, start.RunID); end.Status != "completed" {
		t.Fatalf("turn %s ended %+v", start.RunID, end)
	}
}

// saveInstancesAt is the settings page's inference save, pointed at one
// provider: the same row shape the fixture seeded, with the endpoint
// moved. The credential is the environment's, which keeps the save off
// the OS credential store.
func saveInstancesAt(t *testing.T, f *appBindingFixture, endpoint string) {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "test-key")
	save := NewConfig(f.core)
	if err := save.SaveInstances(InferenceRequest{Instances: []config.InstanceSpec{{
		Type:      "openai",
		Name:      "fake",
		API:       "chat",
		Endpoint:  endpoint,
		KeySource: config.KeySourceEnvName,
		Enabled:   boolPtr(true),
		Models:    []config.ModelSpec{{Name: "fake-model"}},
	}}}); err != nil {
		t.Fatalf("save instances: %v", err)
	}
}

// TestSettingsSaveReachesAnApplication pins the scope a settings save
// reaches. An application carries no provider of its own — its document
// is its layers with the user's inference wiring merged in as the
// overlay — so a save that only reloaded the window's workspace leaves
// every installed application on the provider set it was assembled with,
// which is what the second half of this test would keep reporting.
func TestSettingsSaveReachesAnApplication(t *testing.T) {
	first := fakeprovider.New(t, fakeprovider.Reply{Text: "from A"})
	second := fakeprovider.New(t, fakeprovider.Reply{Text: "from B"})
	f := newAppBinding(t, first)
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
	startAppTurn(t, f, seen, "hello", conversation, "first")
	before := first.Calls()
	if before == 0 {
		t.Fatal("the application's first turn never reached the seeded provider")
	}

	saveInstancesAt(t, f, second.URL())

	startAppTurn(t, f, seen, "hello", conversation, "second")
	if second.Calls() == 0 {
		t.Fatal("the application did not reach the provider the save named")
	}
	if got := first.Calls(); got != before {
		t.Fatalf("the application still ran on the pre-save provider (%d calls, want %d)", got, before)
	}
}

// TestSettingsSaveKeepsTheWorkspaceAndRetiresTheApplications pins the
// in-place branch: with a workspace open, a save swaps that Host's
// document without tearing it down (the window keeps serving), and the
// applications — the half an in-place swap cannot serve — are retired so
// the next turn assembles against the new wiring. A rebuild here would
// show up as a replaced workspace Host; a missing invalidation as a turn
// that reaches the old provider.
func TestSettingsSaveKeepsTheWorkspaceAndRetiresTheApplications(t *testing.T) {
	first := fakeprovider.New(t, fakeprovider.Reply{Text: "from A"})
	second := fakeprovider.New(t, fakeprovider.Reply{Text: "from B"})
	f := newAppBinding(t, first)
	ctx := host.WithAssemblyReason(context.Background(), host.ReasonWorkspaceOpen)
	f.core.SetWorkDir(t.TempDir())
	if err := f.core.RebuildRuntime(ctx); err != nil {
		t.Fatalf("open the workspace: %v", err)
	}
	workspace := f.core.ActiveHost()
	if workspace == nil {
		t.Fatal("no Host assembled for the window's workspace")
	}
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
	startAppTurn(t, f, seen, "hello", conversation, "first")
	before := first.Calls()
	if before == 0 {
		t.Fatal("the application's first turn never reached the seeded provider")
	}

	saveInstancesAt(t, f, second.URL())

	if f.core.ActiveHost() != workspace {
		t.Fatal("the settings save replaced the window's workspace Host; want the in-place swap")
	}
	waitForRetirement(t, f, "hello")
	startAppTurn(t, f, seen, "hello", conversation, "second")
	if second.Calls() == 0 {
		t.Fatal("the application's next turn did not assemble from the saved wiring")
	}
	if got := first.Calls(); got != before {
		t.Fatalf("the application still ran on the pre-save provider (%d calls, want %d)", got, before)
	}
}

// writeAppPlugin writes a minimal installable plugin package: the
// manifest, and the bundle its entry names. Nothing in it has to run —
// what the test needs is a mutation that moves the registry's revision.
func writeAppPlugin(t *testing.T, id string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{"id": "` + id + `", "name": "` + id + `", ` +
		`"version": "1.0.0", "entry": "dist/index.js", "permissions": []}`
	files := map[string]string{
		"plugin.json":   manifest,
		"dist/index.js": "export const apply = () => {};\n",
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

// TestPluginMutationReachesAnApplication pins the other input both
// deployments read. A plugin's providers land in the user's inference
// wiring, which an application reads as the overlay under its layers, so
// the refresh a mutation triggers has to reach the application scope. A
// refresh that stopped at the workspace scope would leave this
// application serving the Host it was assembled into, and the poll below
// is what says so: nothing else would ever ask it for a new one.
func TestPluginMutationReachesAnApplication(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "hi"})
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
	startAppTurn(t, f, seen, "hello", conversation, "first")
	// The retirement below is about a Host that exists: an application
	// nobody assembled has nothing to retire.
	st, err := f.binding.Status("hello")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Serving {
		t.Fatalf("the application is not being served: %+v", st)
	}

	if _, err := f.core.Plugin.Store.Install(writeAppPlugin(t, "providers")); err != nil {
		t.Fatalf("install plugin: %v", err)
	}
	if err := f.core.RefreshPluginRuntime(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	waitForRetirement(t, f, "hello")
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
