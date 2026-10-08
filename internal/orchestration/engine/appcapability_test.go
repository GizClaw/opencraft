package engine

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/tool"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// The capability fragments an application opts into are part of the
// document its runtime is built from — not a promise the host keeps
// somewhere else — so the tests here read the merged document and build
// it. What they pin is the three things that can disagree: which
// fragments the manifest's names select, where their containers land,
// and which agents the wiring reaches.

// secondAppLayer writes one more layer declaring a second agent, the
// shape the manifest's `agents:` lists: its own graph, its own deps.
func secondAppLayer(t *testing.T, content string) {
	t.Helper()
	writeFixtureFile(t, content, "second.yaml", `agents:
  judge:
    card:
      name: Judge
    engine:
      kind: agent.Engine
      impl: graph
      deps:
        inference: infer
        router: router
        workspace: ws
        script_runtime: js
      settings:
        graph: { file: graph.yaml }
`)
}

// containerKeys lists the keys one tool assembly aggregates, sorted: the
// containers the fragments contributed with the core merge's union.
func containerKeys(t *testing.T, doc deploy.Document) []string {
	t.Helper()
	assembly, ok := doc.Resources[config.CapabilityToolsKey]
	if !ok {
		t.Fatalf("the merged document has no %q assembly", config.CapabilityToolsKey)
	}
	out := make([]string, 0, len(assembly.Deps))
	for key := range assembly.Deps {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// TestAppDeploymentCarriesTheCapabilitiesItsManifestEnabled: the
// manifest's names select the fragments, the fragments contribute their
// containers to one assembly, and every agent the application runs is
// wired to it — including the ones the manifest lists beyond the entry,
// because a dependency is per agent.
//
// The same package without the names is the control: the contract
// layer's surface and nothing more, which is what makes the manifest's
// list the whole request.
func TestAppDeploymentCarriesTheCapabilitiesItsManifestEnabled(t *testing.T) {
	ctx := context.Background()
	content := writeAppContent(t, "Hello")
	secondAppLayer(t, content)
	userDir := t.TempDir()

	doc, err := LoadAppDocument(ctx, AppDoc{
		ID:           "hello",
		ContentDir:   content,
		Layers:       []string{"layer.yaml", "second.yaml"},
		Capabilities: []string{"tools", "web"},
		Agents:       []string{"app", "judge"},
	}, userDir)
	if err != nil {
		t.Fatalf("LoadAppDocument: %v", err)
	}

	got := containerKeys(t, doc)
	want := []string{"tool.applypatch", "tool.files", "tool.viewimage", "tool.webfetch", "tool.websearch"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("containers = %v, want %v (the two enabled fragments, and nothing else)", got, want)
	}
	if doc.Resources["tools"].Kind != "tool.Assembly" {
		t.Errorf("the assembly's kind = %q, want the host's own tool.Assembly", doc.Resources["tools"].Kind)
	}
	// Each fragment's own resources arrive with it, and a fragment the
	// manifest did not name brings nothing: the network gate belongs to
	// `web`, the sandbox's approvals to `exec`.
	if _, ok := doc.Resources["netpolicy"]; !ok {
		t.Error("the web fragment's network policy is missing")
	}
	if _, ok := doc.Resources["execpolicy"]; ok {
		t.Error("the exec fragment's approvals arrived without the manifest naming it")
	}
	// Naming it is the whole request, so the third fragment's resources
	// arrive with it and nothing else about the document changes.
	all, err := LoadAppDocument(ctx, AppDoc{
		ID:           "hello",
		ContentDir:   content,
		Layers:       []string{"layer.yaml", "second.yaml"},
		Capabilities: config.CapabilityNames(),
		Agents:       []string{"app", "judge"},
	}, userDir)
	if err != nil {
		t.Fatalf("LoadAppDocument with every capability: %v", err)
	}
	withExec := containerKeys(t, all)
	if !hasName(withExec, "tool.exec") {
		t.Errorf("containers = %v, want the third fragment's tool.exec among them", withExec)
	}
	for _, key := range []string{"execpolicy", "box"} {
		if _, ok := all.Resources[key]; !ok {
			t.Errorf("the exec fragment's %q is missing", key)
		}
	}
	// The wiring reaches every agent the application runs — the second
	// agent as well as the entry, since its engine is a different one.
	for _, name := range []string{"app", "judge"} {
		if got := doc.Agents[name].Engine.Deps[config.CapabilityToolsKey]; got != config.CapabilityToolsKey {
			t.Errorf("agents.%s.engine.deps.tools = %q, want %q (every agent the application runs is wired)",
				name, got, config.CapabilityToolsKey)
		}
	}

	// The control: the same package, the manifest asking for nothing.
	bare, err := LoadAppDocument(ctx, AppDoc{
		ID:         "hello",
		ContentDir: content,
		Layers:     []string{"layer.yaml", "second.yaml"},
		Agents:     []string{"app", "judge"},
	}, userDir)
	if err != nil {
		t.Fatalf("LoadAppDocument without capabilities: %v", err)
	}
	if _, ok := bare.Resources[config.CapabilityToolsKey]; ok {
		t.Error("a document without capabilities carries the tool assembly")
	}
	for _, name := range []string{"app", "judge"} {
		if dep := bare.Agents[name].Engine.Deps[config.CapabilityToolsKey]; dep != "" {
			t.Errorf("agents.%s.engine.deps.tools = %q without the capability", name, dep)
		}
		if bare.Agents[name].Card.Name != doc.Agents[name].Card.Name {
			t.Errorf("agents.%s's card differs between the two documents", name)
		}
	}
}

// TestAppDeploymentBuildsTheFragmentsAndCataloguesTheirTools is the "an
// application that installs is an application that assembles" half,
// applied to the fragments: the fragments a runtime can be built for in
// a test produce one, and the containers they declared are what the
// assembly actually registers — which is the model-facing half a
// document assertion cannot see.
//
// The `exec` fragment is deliberately absent: its sandbox spawns the
// host's execd child at assembly (the parent never runs sandboxed code
// in-process, for the assistant and for an application alike), and a
// unit test has no binary to fork. What that fragment can be checked for
// here is its shape — the containers it contributes and the resources it
// declares — which TestCapabilityTableMatchesTheEmbeddedFragments reads
// off the layer, and its kind/impl pairs are the assistant's own.
func TestAppDeploymentBuildsTheFragmentsAndCataloguesTheirTools(t *testing.T) {
	ctx := context.Background()
	content := writeAppContent(t, "Hello")
	userDir := writeUserInference(t, t.TempDir())
	dataDir := t.TempDir()

	names := []string{"tools", "web"}
	doc, err := LoadAppDocument(ctx, AppDoc{
		ID:           "hello",
		ContentDir:   content,
		Layers:       []string{"layer.yaml"},
		Capabilities: names,
		Agents:       []string{"app"},
	}, userDir)
	if err != nil {
		t.Fatalf("LoadAppDocument: %v", err)
	}

	layout, err := config.AppLayout(dataDir, "hello")
	if err != nil {
		t.Fatalf("AppLayout: %v", err)
	}
	store := appSessionStore(t, layout)
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
		t.Fatalf("BuildRuntime with every capability enabled: %v", err)
	}
	defer func() { _ = rt.Close() }()

	value, ok := rt.Resource(config.CapabilityToolsKey)
	if !ok {
		t.Fatalf("the runtime built no %q resource", config.CapabilityToolsKey)
	}
	assembly, ok := value.(*tool.Assembly)
	if !ok {
		t.Fatalf("%q built as %T, want the host's tool assembly", config.CapabilityToolsKey, value)
	}
	// The model-facing question the last test's document could not
	// answer: the containers the fragments declared actually registered
	// with the assembly the agents dispatch through.
	registered := make([]string, 0, len(assembly.Catalog().Definitions()))
	for _, definition := range assembly.Catalog().Definitions() {
		registered = append(registered, definition.Name)
	}
	for _, want := range []string{"read_file", "write_file", "list_dir", "web_fetch", "web_search"} {
		if !hasName(registered, want) {
			t.Errorf("the assembler registered %v, want %q among them", registered, want)
		}
	}
	// And what the manifest did not name is not there: the sandbox's own
	// tool is the one the `exec` fragment contributes, and this
	// application never asked for it.
	if hasName(registered, "exec_command") {
		t.Errorf("the assembler registered %v, want no exec_command without the exec capability", registered)
	}
}

// hasName reports whether one of the registered names is want.
func hasName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// TestAppDeploymentRefusesCapabilitiesItCannotWire: the fragment stack
// is built from two lists that come from different files (the manifest's
// names, the manifest's agents), and the wiring layer is about the
// second. A name this build does not ship, or an application that names
// a fragment but no agent to wire it to, fails at the document — which
// is where the registry's preflight reads the same stack, so the
// refusal an author gets at install and the one they get at assembly are
// the same sentence.
func TestAppDeploymentRefusesCapabilitiesItCannotWire(t *testing.T) {
	ctx := context.Background()
	content := writeAppContent(t, "Hello")
	userDir := t.TempDir()

	cases := []struct {
		name string
		app  AppDoc
		want string
	}{
		{
			name: "unknown name",
			app: AppDoc{
				ID: "hello", ContentDir: content, Layers: []string{"layer.yaml"},
				Capabilities: []string{"sudo"}, Agents: []string{"app"},
			},
			want: `"sudo" is not a capability this build provides`,
		},
		{
			name: "no agent to wire",
			app: AppDoc{
				ID: "hello", ContentDir: content, Layers: []string{"layer.yaml"},
				Capabilities: []string{"tools"},
			},
			want: "1 capabilities enabled but no agent to wire them into",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadAppDocument(ctx, tc.app, userDir); err == nil {
				t.Fatal("the document was assembled")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to read %q", err, tc.want)
			}
		})
	}

	// The control: the same document with a name this build ships and one
	// agent to wire it to assembles, so the two refusals above are about
	// the list, not about the fixture.
	if _, err := LoadAppDocument(ctx, AppDoc{
		ID: "hello", ContentDir: content, Layers: []string{"layer.yaml"},
		Capabilities: []string{"tools"}, Agents: []string{"app"},
	}, userDir); err != nil {
		t.Fatalf("the control document failed: %v", err)
	}
}
