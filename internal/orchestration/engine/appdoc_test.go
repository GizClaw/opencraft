package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/capabilities/apps"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

// writeFixtureFile writes one fixture file under dir, creating parents.
func writeFixtureFile(t *testing.T, dir, rel, data string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeAppLayer writes the minimal application layer into dir: a card
// merged into the reserved agent slot, and a graph reference relative to
// dir.
func writeAppLayer(t *testing.T, dir, card string) {
	t.Helper()
	writeFixtureFile(t, dir, "layer.yaml", `agents:
  app:
    card:
      name: `+card+`
      description: minimal application fixture
    engine:
      settings:
        graph: { file: graph.yaml }
`)
}

// writeAppGraph writes the graph the fixture layer points at, and the
// script node it runs.
func writeAppGraph(t *testing.T, dir string) {
	t.Helper()
	writeFixtureFile(t, dir, "graph.yaml", `name: hello
entry: hello
nodes:
  - id: hello
    type: script
    config:
      runtime: js
      source: { file: nodes/hello.js }
edges:
  - { from: hello, to: __end__ }
`)
	writeFixtureFile(t, dir, "nodes/hello.js", `board.appendChannel(board.MAIN_CHANNEL, {
  role: "assistant",
  content: "hello",
});
`)
}

// writeAppContent writes the whole fixture into one directory — layer,
// graph and node script — so every {file:} reference resolves inside it.
// That is what lets the build prove the loader base (FileBase).
func writeAppContent(t *testing.T, card string) string {
	t.Helper()
	dir := t.TempDir()
	writeAppLayer(t, dir, card)
	writeAppGraph(t, dir)
	return dir
}

// appSessionStore opens the migrated store an application layout owns.
func appSessionStore(t *testing.T, layout config.WorkspaceLayout) *ocsessions.Store {
	t.Helper()
	if err := layout.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	store, err := sessionstore.Open(t, layout.SessionsDir, 40)
	if err != nil {
		t.Fatalf("open migrated sessions: %v", err)
	}
	return store
}

// resolveSymlinks resolves path for comparison, failing the test when
// the path does not exist (which would make the comparison meaningless).
func resolveSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}

// writeUserInference seeds the user layer the way the first-run wizard
// does into userDir, next to a local-sandbox override that must not
// follow the inference wiring into an application deployment.
func writeUserInference(t *testing.T, userDir string) string {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "test-key")
	seedLocalSandboxConfig(t, userDir)
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models:    []config.Model{{Name: "test-model"}},
		}},
	}
	if err := configseed.Write(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}
	return userDir
}

// TestAppDeploymentAssemblesFromThreeLayers is the P0 acceptance for
// this step: a minimal application — contract layer, one application
// layer, the inference overlay — assembles a runtime with no host, and
// the state values the resolver carries are the application's own
// (private workspace, private session store), never a project path.
func TestAppDeploymentAssemblesFromThreeLayers(t *testing.T) {
	ctx := context.Background()
	content := writeAppContent(t, "Hello")
	userDir := writeUserInference(t, t.TempDir())
	dataDir := t.TempDir()

	doc, err := LoadAppDocument(ctx, AppDoc{
		ID:         "hello",
		ContentDir: content,
		Layers:     []string{"layer.yaml"},
	}, userDir)
	if err != nil {
		t.Fatalf("LoadAppDocument: %v", err)
	}

	// The contract layer is present, the application layer merged into
	// its reserved agent slot, and the overlay is the only source of
	// inference — the sandbox override in the same user file stayed out.
	for _, key := range []string{"events", "sessions", "artifacts", "ws", "mem", "observer", "js", "router", "infer"} {
		if _, ok := doc.Resources[key]; !ok {
			t.Errorf("merged document is missing %q", key)
		}
	}
	if _, ok := doc.Resources["box"]; ok {
		t.Error("the user layer's sandbox override leaked into the application document")
	}
	definition, ok := doc.Agents["app"]
	if !ok {
		t.Fatal("the reserved agent slot is missing from the merged document")
	}
	if definition.Card.Name != "Hello" {
		t.Errorf("agent card name = %q, want the application layer's Hello", definition.Card.Name)
	}
	if definition.Policy.RunTimeout != "2h" {
		t.Errorf("agent run timeout = %q, want the contract layer's 2h", definition.Policy.RunTimeout)
	}

	layout, err := config.AppLayout(dataDir, "hello")
	if err != nil {
		t.Fatalf("AppLayout: %v", err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	store, err := sessionstore.Open(t, layout.SessionsDir, 40)
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	rt, err := BuildRuntime(
		ctx,
		doc,
		WithWorkBase(layout.WorkDir),
		WithFileBase(content),
		WithConfigBase(userDir),
		WithWorkspaceLayout(&layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return store, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	defer func() { _ = rt.Close() }()

	application, ok := rt.Agent("app")
	if !ok {
		t.Fatal("the reserved application agent did not build")
	}
	if application.Policy.RunTimeout != "2h" {
		t.Errorf("assembled run timeout = %q, want 2h", application.Policy.RunTimeout)
	}
	// ${ocraft:WORKDIR} is the application's private workspace: the
	// resolver value comes from the layout, not from a process-wide
	// default and not from the content root.
	value, ok := rt.Resource("ws")
	if !ok {
		t.Fatal("the workspace resource did not build")
	}
	workspace, ok := value.(interface{ Root() string })
	if !ok {
		t.Fatalf("workspace resource is %T, want one with a Root()", value)
	}
	// The workspace resolves its root; the layout keeps the spelling it
	// was given (on macOS a temp dir is /var -> /private/var).
	if got, want := resolveSymlinks(t, workspace.Root()), resolveSymlinks(t, layout.WorkDir); got != want {
		t.Errorf("workspace root = %q, want the application's %q", got, want)
	}
	if value, ok := rt.Resource("sessions"); !ok {
		t.Error("the session store resource did not build")
	} else if got := value.(*ocsessions.Store); got != store {
		t.Error("the session resource is not the store the assembly was given")
	}
}

// TestAppLayersOverrideInOrderAndTheOverlayLast pins the priority
// order: application layers merge in manifest order, and the inference
// overlay sits above all of them. An application layer naming a
// reserved inference key is rejected by the registry's validation
// before it ever gets here; the loader still must not let it win.
func TestAppLayersOverrideInOrderAndTheOverlayLast(t *testing.T) {
	ctx := context.Background()
	content := writeAppContent(t, "First")
	if err := os.WriteFile(
		filepath.Join(content, "second.yaml"),
		[]byte(`agents:
  app:
    card:
      name: Second
resources:
  router:
    kind: agent.Engine
    impl: graph
`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	userDir := writeUserInference(t, t.TempDir())
	doc, err := LoadAppDocument(ctx, AppDoc{
		ID:         "hello",
		ContentDir: content,
		Layers:     []string{"layer.yaml", "second.yaml"},
	}, userDir)
	if err != nil {
		t.Fatalf("LoadAppDocument: %v", err)
	}
	if got := doc.Agents["app"].Card.Name; got != "Second" {
		t.Errorf("agent card name = %q, want the later layer's Second", got)
	}
	router, ok := doc.Resources["router"]
	if !ok {
		t.Fatal("router did not come from the overlay")
	}
	if router.Kind != "inference.Router" {
		t.Errorf("router kind = %q, want the overlay's inference.Router", router.Kind)
	}
}

// TestAppDeploymentWithoutInferencePointsAtTheSettingsPage pins the
// failure a user sees before the wizard ran: the application document
// assembles (it simply has no router), and BuildRuntime names the file
// the settings page writes.
func TestAppDeploymentWithoutInferencePointsAtTheSettingsPage(t *testing.T) {
	ctx := context.Background()
	content := writeAppContent(t, "Hello")
	userDir := t.TempDir()
	doc, err := LoadAppDocument(ctx, AppDoc{
		ID:         "hello",
		ContentDir: content,
		Layers:     []string{"layer.yaml"},
	}, userDir)
	if err != nil {
		t.Fatalf("LoadAppDocument: %v", err)
	}
	if _, ok := doc.Resources["router"]; ok {
		t.Fatal("a document without an overlay declared a router")
	}
	layout, err := config.AppLayout(t.TempDir(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	store := appSessionStore(t, layout)
	_, err = BuildRuntime(
		ctx,
		doc,
		WithWorkBase(layout.WorkDir),
		WithFileBase(content),
		WithConfigBase(userDir),
		WithWorkspaceLayout(&layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return store, nil
		}),
	)
	if err == nil {
		t.Fatal("BuildRuntime succeeded without inference configured")
	}
	for _, want := range []string{"inference is not configured", "settings page"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestAppDeploymentWithoutAGraphFailsLoudly pins the other half of the
// "no silent default" rule: the contract layer leaves engine.graph
// unset, and a missing graph must fail assembly instead of pointing at
// a document nobody wrote.
func TestAppDeploymentWithoutAGraphFailsLoudly(t *testing.T) {
	ctx := context.Background()
	content := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(content, "layer.yaml"),
		[]byte("agents:\n  app:\n    card:\n      name: NoGraph\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	userDir := writeUserInference(t, t.TempDir())
	doc, err := LoadAppDocument(ctx, AppDoc{
		ID:         "hello",
		ContentDir: content,
		Layers:     []string{"layer.yaml"},
	}, userDir)
	if err != nil {
		t.Fatalf("LoadAppDocument: %v", err)
	}
	layout, err := config.AppLayout(t.TempDir(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	store := appSessionStore(t, layout)
	_, err = BuildRuntime(
		ctx,
		doc,
		WithWorkBase(layout.WorkDir),
		WithFileBase(content),
		WithConfigBase(userDir),
		WithWorkspaceLayout(&layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return store, nil
		}),
	)
	if err == nil {
		t.Fatal("BuildRuntime succeeded with no graph declared")
	}
	if !strings.Contains(err.Error(), "graph") {
		t.Errorf("error %q does not name the missing graph", err)
	}
}

// TestLoadAppDocumentRefusesLayersOutsideTheContentRoot pins the join
// onto the content root: a layer name that could walk out of it (or an
// absolute path) is refused here, whatever the caller believes it
// validated.
func TestLoadAppDocumentRefusesLayersOutsideTheContentRoot(t *testing.T) {
	ctx := context.Background()
	content := writeAppContent(t, "Hello")
	cases := []struct {
		name  string
		layer string
	}{
		{"parent traversal", "../layer.yaml"},
		{"absolute path", "/etc/opencraft.yaml"},
		{"empty name", "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadAppDocument(ctx, AppDoc{
				ID:         "hello",
				ContentDir: content,
				Layers:     []string{tc.layer},
			}, t.TempDir())
			if err == nil {
				t.Fatalf("LoadAppDocument accepted layer %q", tc.layer)
			}
			if !strings.Contains(err.Error(), "inside the content root") {
				t.Errorf("error %q does not explain the content-root rule", err)
			}
		})
	}
	// A document with no layers at all is not an application.
	if _, err := LoadAppDocument(ctx, AppDoc{ID: "hello", ContentDir: content}, t.TempDir()); err == nil {
		t.Error("LoadAppDocument accepted an application with no layers")
	}
	// And a missing content dir is the caller's bug, not a file error:
	// the refusal has to name it, or the caller reads a "no such file"
	// for a layer it never meant to load from the current directory.
	_, err := LoadAppDocument(ctx, AppDoc{ID: "hello", Layers: []string{"layer.yaml"}}, t.TempDir())
	if err == nil {
		t.Fatal("LoadAppDocument accepted an empty content dir")
	}
	if !strings.Contains(err.Error(), "content dir") {
		t.Errorf("error %q does not name the missing content dir", err)
	}
}

// TestAppContractLayerStaysMinimal pins the shipped contract layer: the
// exact resource set a v1 application runtime starts from, and no more.
// Widening it — by adding a tool container, a sandbox, a skill registry
// — is the change this list exists to make visible, and every kind it
// does declare has to be a row in the application policy table (denied
// rows included: the credential view and the transcript hooks are the
// host's, and an application layer may not redeclare them).
func TestAppContractLayerStaysMinimal(t *testing.T) {
	ctx := context.Background()
	doc, _, err := deploy.LoadLayers(ctx, []deploy.Layer{config.AppContractLayer()})
	if err != nil {
		t.Fatalf("load contract layer: %v", err)
	}
	wantKeys := []string{
		"artifacts", "events", "js", "mem", "observer", "secret.keychain",
		"sessions", "ws",
	}
	if len(doc.Resources) != len(wantKeys) {
		t.Errorf("contract layer resources = %v, want exactly %v",
			keysOfResources(doc.Resources), wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := doc.Resources[key]; !ok {
			t.Errorf("contract layer is missing resource %q", key)
		}
	}
	classified := func(what, kind string) {
		t.Helper()
		_, ok := apps.Classify(kind)
		if !ok {
			t.Errorf("%s: kind %q is not in the application allow/deny table", what, kind)
		}
	}
	if len(doc.Resources) == 0 {
		t.Fatal("the contract layer declares no resources")
	}
	for name, res := range doc.Resources {
		classified("resource "+name, string(res.Kind))
	}
	for name, definition := range doc.Agents {
		classified("agent "+name+" engine", string(definition.Engine.Kind))
		for _, slot := range []struct {
			name  string
			hooks []agent.Hook
		}{
			{agent.HookSlotPreparer, definition.Prepare},
			{agent.HookSlotObserver, definition.Observe},
			{agent.HookSlotCommitter, definition.Commit},
		} {
			for i, hook := range slot.hooks {
				classified(name+" hook "+slot.name, "hook."+slot.name)
				if strings.TrimSpace(hook.Type) == "" {
					t.Errorf("agent %s %s[%d] has no type", name, slot.name, i)
				}
			}
		}
	}
}

// TestFileBaseDefaultsToConfigBase pins the historical single root: with
// no FileBase given, every {file:} reference inside the document resolves
// against ConfigBase. The fixture splits the two on purpose — the layer
// lives in the content dir, the graph it points at lives in the config
// dir — so the build can only succeed through the default.
func TestFileBaseDefaultsToConfigBase(t *testing.T) {
	ctx := context.Background()
	content := t.TempDir()
	writeAppLayer(t, content, "Hello")
	userDir := writeUserInference(t, t.TempDir())
	writeAppGraph(t, userDir)

	doc, err := LoadAppDocument(ctx, AppDoc{
		ID:         "hello",
		ContentDir: content,
		Layers:     []string{"layer.yaml"},
	}, userDir)
	if err != nil {
		t.Fatalf("LoadAppDocument: %v", err)
	}
	layout, err := config.AppLayout(t.TempDir(), "hello")
	if err != nil {
		t.Fatalf("AppLayout: %v", err)
	}
	store := appSessionStore(t, layout)
	rt, err := BuildRuntime(
		ctx,
		doc,
		WithWorkBase(layout.WorkDir),
		WithConfigBase(userDir),
		WithWorkspaceLayout(&layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return store, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime without FileBase: %v", err)
	}
	defer func() { _ = rt.Close() }()
	if _, ok := rt.Agent("app"); !ok {
		t.Error("the reserved application agent did not build")
	}
}

// TestAppDeploymentRequiresASessionStore pins where the guard lives now:
// BuildRuntime validates the store, the factory list itself registers
// with zero options (that is what makes KnownKinds possible). The
// failure still names the compat migration, so a caller that drops
// WithSessionStore reads the same sentence it read before.
func TestAppDeploymentRequiresASessionStore(t *testing.T) {
	ctx := context.Background()
	content := writeAppContent(t, "Hello")
	userDir := writeUserInference(t, t.TempDir())
	doc, err := LoadAppDocument(ctx, AppDoc{
		ID:         "hello",
		ContentDir: content,
		Layers:     []string{"layer.yaml"},
	}, userDir)
	if err != nil {
		t.Fatalf("LoadAppDocument: %v", err)
	}
	layout, err := config.AppLayout(t.TempDir(), "hello")
	if err != nil {
		t.Fatalf("AppLayout: %v", err)
	}
	_, err = BuildRuntime(
		ctx,
		doc,
		WithWorkBase(layout.WorkDir),
		WithFileBase(content),
		WithConfigBase(userDir),
		WithWorkspaceLayout(&layout),
	)
	if err == nil {
		t.Fatal("BuildRuntime assembled an application with no session store")
	}
	if !strings.Contains(err.Error(), "WithSessionStore") {
		t.Errorf("error %q does not point at WithSessionStore", err)
	}
}

func keysOfResources(res resource.Resources) []string {
	keys := make([]string, 0, len(res))
	for key := range res {
		keys = append(keys, key)
	}
	return keys
}
