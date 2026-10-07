package apps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture application: a manifest, one layer that merges into the
// contract's reserved agent slot, the graph that layer references, and
// the script the graph runs. It is the smallest package the preflight
// accepts, so every test below can change exactly one thing and read the
// refusal that names it.
const (
	fixtureManifest = `app: v1
id: hello
name: Hello
version: 0.1.0
layers:
  - layer.yaml
`
	fixtureLayer = `agents:
  app:
    card:
      name: Hello
      description: minimal application fixture
    engine:
      settings:
        graph: { file: graph.yaml }
`
	fixtureGraph = `name: hello
entry: hello
nodes:
  - id: hello
    type: script
    config:
      runtime: js
      source: { file: nodes/hello.js }
edges:
  - { from: hello, to: __end__ }
`
	fixtureScript = `board.appendChannel(board.MAIN_CHANNEL, {
  role: "assistant",
  content: "hello",
});
`
)

// writeTestFile writes one fixture file, creating parents.
func writeTestFile(t *testing.T, dir, rel, data string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newApp writes the complete fixture application into a fresh directory
// and returns it.
func newApp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, ManifestFile, fixtureManifest)
	writeTestFile(t, dir, "layer.yaml", fixtureLayer)
	writeTestFile(t, dir, "graph.yaml", fixtureGraph)
	writeTestFile(t, dir, "nodes/hello.js", fixtureScript)
	return dir
}

// appFor reads one written fixture the way the install path does: the
// manifest decides the id, the content root, the layers and the entry
// agent.
func appFor(t *testing.T, dir string) App {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	app := App{
		ID:         m.ID,
		Name:       m.Name,
		Version:    m.Version,
		Icon:       m.Icon,
		Agent:      m.Agent,
		ContentDir: dir,
		Layers:     append([]string(nil), m.Layers...),
	}
	if m.UI != nil {
		app.UI = *m.UI
	}
	return app
}

// validate refuses or accepts one written fixture.
func validate(t *testing.T, dir string) error {
	t.Helper()
	return Validate(context.Background(), appFor(t, dir))
}

// refusalsOf returns the refusal list of a failed preflight, failing the
// test when the error is not a refusal (a host-side error is a bug, not
// a verdict).
func refusalsOf(t *testing.T, err error) Refusals {
	t.Helper()
	if err == nil {
		t.Fatal("the preflight accepted the fixture")
	}
	var refusals Refusals
	if !errors.As(err, &refusals) {
		t.Fatalf("error %v is not a Refusals", err)
	}
	return refusals
}

// refusedEvery asserts that one refusal mentions every fragment, so a
// test names the whole sentence it is about.
func refusedEvery(t *testing.T, refusals Refusals, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(refusals.Error(), fragment) {
			t.Errorf("refusals do not mention %q:\n%s", fragment, refusals.Error())
		}
	}
}

// TestValidateAcceptsAMinimalApplication is the other half of every
// refusal test below: the fixture is a real application, so a refusal it
// triggers is about the change the test made.
func TestValidateAcceptsAMinimalApplication(t *testing.T) {
	if err := validate(t, newApp(t)); err != nil {
		t.Fatalf("the minimal application was refused: %v", err)
	}
}

// TestValidateRefusesKeysTheContractLayerProvides walks the resources
// the contract layer declares: an application layer that declares one is
// refused by name, whichever name it proposes, because the value of the
// key is that the host owns the resource.
func TestValidateRefusesKeysTheContractLayerProvides(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `resources:
  events:
    kind: event.Bus
    impl: memory
  ws:
    kind: opencraft.workspace
    impl: local
  js:
    kind: agent.ScriptRuntime
    impl: js
agents:
  app:
    card:
      name: Hello
    engine:
      settings:
        graph: { file: graph.yaml }
`)
	refusals := refusalsOf(t, validate(t, dir))
	if len(refusals.List) != 3 {
		t.Fatalf("refusals = %d, want 3:\n%s", len(refusals.List), refusals.Error())
	}
	refusedEvery(t, refusals,
		"layer.yaml: resources.events: the host provides \"events\" in the contract layer",
		"layer.yaml: resources.ws: the host provides \"ws\" in the contract layer",
		"layer.yaml: resources.js: the host provides \"js\" in the contract layer")
}

// TestValidateRefusesASecondStoreOfEveryContractKind pins the rule that
// a layer cannot escape the reserved names by picking another one: the
// kinds the contract layer already builds are refused as kinds, so a
// second history has no spelling.
func TestValidateRefusesASecondStoreOfEveryContractKind(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `resources:
  sessions2:
    kind: session.Store
    impl: opencraft
  mem2:
    kind: memory
    impl: summary
  bus2:
    kind: event.Bus
    impl: memory
  ws2:
    kind: workspace.Workspace
    impl: local
  sink2:
    kind: opencraft.artifacts
    impl: local
  keys2:
    kind: secret.Store
    impl: keychain
agents:
  app:
    engine:
      settings:
        graph: { file: graph.yaml }
`)
	refusals := refusalsOf(t, validate(t, dir))
	if len(refusals.List) != 6 {
		t.Fatalf("refusals = %d, want 6:\n%s", len(refusals.List), refusals.Error())
	}
	refusedEvery(t, refusals,
		"resources.sessions2: kind \"session.Store\" is not available to an application: the contract layer owns the application's session store; a second one would be a second history",
		"resources.mem2: kind \"memory\" is not available to an application",
		"resources.bus2: kind \"event.Bus\" is not available to an application",
		"resources.ws2: kind \"workspace.Workspace\" is not available to an application: an application has one private workspace",
		"resources.sink2: kind \"opencraft.artifacts\" is not available to an application",
		"resources.keys2: kind \"secret.Store\" is not available to an application")
}

// TestValidateRefusesInferenceWiringByKey keeps the overlay's keys out of
// an application layer: the user's settings page owns them, and a layer
// that declared them would be carrying credentials.
func TestValidateRefusesInferenceWiringByKey(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `resources:
  infer:
    kind: inference.Assembly
    impl: openai
  router:
    kind: inference.Router
    impl: single
  provider.deepseek:
    kind: inference.Provider
    impl: openai
agents:
  app:
    engine:
      settings:
        graph: { file: graph.yaml }
`)
	refusals := refusalsOf(t, validate(t, dir))
	if len(refusals.List) != 3 {
		t.Fatalf("refusals = %d, want 3:\n%s", len(refusals.List), refusals.Error())
	}
	refusedEvery(t, refusals,
		"layer.yaml: resources.infer: inference wiring is generated from the user's settings",
		"layer.yaml: resources.router: inference wiring is generated from the user's settings",
		"layer.yaml: resources.provider.deepseek: inference wiring is generated from the user's settings")
}

// TestValidateRefusesExecutionSurfacesAndUnknownKinds covers the two
// ways a kind can be unusable: the table denies it (v1 gives an
// application no exec), or this build has no factory for it at all.
func TestValidateRefusesExecutionSurfacesAndUnknownKinds(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `resources:
  shell:
    kind: sandbox.Runner
    impl: bwrap
  nodes:
    kind: graph.NodeType
    impl: custom
  mystery:
    kind: opencraft.new_thing
    impl: whatever
agents:
  app:
    engine:
      settings:
        graph: { file: graph.yaml }
`)
	refusals := refusalsOf(t, validate(t, dir))
	if len(refusals.List) != 3 {
		t.Fatalf("refusals = %d, want 3:\n%s", len(refusals.List), refusals.Error())
	}
	refusedEvery(t, refusals,
		"resources.shell: kind \"sandbox.Runner\" is not available to an application: v1 gives applications no exec",
		"resources.nodes: kind \"graph.NodeType\" is not available to an application",
		"resources.mystery: kind \"opencraft.new_thing\" is not a resource kind this build can construct")
}

// TestValidateRefusesTheRuntimeSectionAndADifferentVersion pins the two
// document-level declarations an application layer may not make: the
// runtime wiring belongs to the contract layer, and the version is the
// contract's.
func TestValidateRefusesTheRuntimeSectionAndADifferentVersion(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `version: v2
runtime:
  event_bus: events
  checkpoint_store: sessions
agents:
  app:
    engine:
      settings:
        graph: { file: graph.yaml }
`)
	refusals := refusalsOf(t, validate(t, dir))
	refusedEvery(t, refusals,
		"layer.yaml: version: an application layer cannot re-version the document (the contract layer is \"v1\")",
		"layer.yaml: runtime: the runtime section belongs to the contract layer")
}

// TestValidateRefusesAgentsOtherThanTheReservedSlot pins v1's single
// agent: the contract layer's slot is the one that carries the
// transcript path, so another agent would be a turn nobody records.
func TestValidateRefusesAgentsOtherThanTheReservedSlot(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `agents:
  assistant:
    card:
      name: Assistant
  werewolf:
    card:
      name: Werewolf
  app:
    card:
      name: Hello
    engine:
      settings:
        graph: { file: graph.yaml }
`)
	refusals := refusalsOf(t, validate(t, dir))
	if len(refusals.List) != 2 {
		t.Fatalf("refusals = %d, want 2:\n%s", len(refusals.List), refusals.Error())
	}
	refusedEvery(t, refusals,
		"layer.yaml: agents.assistant: v1 assembles exactly one agent",
		"layer.yaml: agents.werewolf: v1 assembles exactly one agent")
}

// TestValidateRefusesAManifestAgentThatIsNotTheReservedSlot: the
// manifest may name the entry agent, and in v1 the only name that works
// is the contract's slot.
func TestValidateRefusesAManifestAgentThatIsNotTheReservedSlot(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, ManifestFile, `app: v1
id: hello
name: Hello
version: 0.1.0
agent: werewolf
layers:
  - layer.yaml
`)
	writeTestFile(t, dir, "layer.yaml", `agents:
  werewolf:
    card:
      name: Werewolf
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
	refusals := refusalsOf(t, validate(t, dir))
	refusedEvery(t, refusals,
		`app.yaml: agent: v1 assembles exactly one agent: the contract layer's "app" slot, which carries the transcript path (commit, observe); the manifest names "werewolf"`,
		`agents.werewolf: v1 assembles exactly one agent`)
}

// TestValidateRefusesHookSlotsAndToolsOnTheReservedAgent pins what a
// layer may merge into the reserved slot: the card, the graph, build and
// policy. Hooks are the transcript path (a layer that redeclared the
// slot could empty it), and tools do not exist in v1.
func TestValidateRefusesHookSlotsAndToolsOnTheReservedAgent(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `agents:
  app:
    card:
      name: Hello
    tools:
      - shell
    commit:
      - type: opencraft.commit
    engine:
      settings:
        graph: { file: graph.yaml }
`)
	refusals := refusalsOf(t, validate(t, dir))
	if len(refusals.List) != 2 {
		t.Fatalf("refusals = %d, want 2:\n%s", len(refusals.List), refusals.Error())
	}
	refusedEvery(t, refusals,
		"layer.yaml: agents.app.commit: the transcript path (prepare, observe, commit) is the contract layer's",
		"layer.yaml: agents.app.tools: v1 gives applications no tools")
}

// TestValidateRefusesADepThatNamesNothing catches the typo the assembly
// would otherwise report as an unbuildable graph, at install time and
// with the layer named.
func TestValidateRefusesADepThatNamesNothing(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `agents:
  app:
    card:
      name: Hello
    engine:
      deps:
        workspace: ws1
        script_runtime: js
      settings:
        graph: { file: graph.yaml }
`)
	refusals := refusalsOf(t, validate(t, dir))
	if len(refusals.List) != 1 {
		t.Fatalf("refusals = %d, want 1:\n%s", len(refusals.List), refusals.Error())
	}
	refusedEvery(t, refusals,
		`layer.yaml: agents.app.engine.deps.workspace: "ws1" names no resource in the merged document`)
}

// TestValidateAcceptsADepOnTheHostsInferenceWiring: infer and router are
// absent from the merged document until the user configures a provider,
// and that state is the settings page's business, not a refusal here.
func TestValidateAcceptsADepOnTheHostsInferenceWiring(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `agents:
  app:
    card:
      name: Hello
    engine:
      deps:
        inference: infer
        router: router
      settings:
        graph: { file: graph.yaml }
`)
	if err := validate(t, dir); err != nil {
		t.Fatalf("a dep on the overlay was refused: %v", err)
	}
}

// TestValidateRequiresTheEntryAgentsGraph pins the one settings key an
// application cannot leave out: the host refuses to assemble a graph
// engine that points at nothing (the contract layer sets no default).
func TestValidateRequiresTheEntryAgentsGraph(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `agents:
  app:
    card:
      name: Hello
`)
	refusals := refusalsOf(t, validate(t, dir))
	refusedEvery(t, refusals,
		"agents.app.engine.settings.graph: the entry agent declares no graph")
}

// TestValidateFollowsAnInlineGraph: an application may inline its graph
// instead of referencing a file (a single-file package), and the
// references inside the inlined document are checked like any other.
func TestValidateFollowsAnInlineGraph(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `agents:
  app:
    card:
      name: Hello
    engine:
      settings:
        graph: |
          name: hello
          entry: hello
          nodes:
            - id: hello
              type: script
              config:
                runtime: js
                source: { file: nodes/missing.js }
`)
	refusals := refusalsOf(t, validate(t, dir))
	refusedEvery(t, refusals,
		"nodes/missing.js: the referenced file does not exist inside the content root")
}

// TestValidateFollowsReferencesInsideAGraphDocument names where the
// scan found the reference: a graph referencing a node script that is
// not there reads as the graph's problem, not the layer's.
func TestValidateFollowsReferencesInsideAGraphDocument(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "graph.yaml", `name: hello
entry: hello
nodes:
  - id: hello
    type: script
    config:
      runtime: js
      source: { file: nodes/missing.js }
edges:
  - { from: hello, to: __end__ }
`)
	refusals := refusalsOf(t, validate(t, dir))
	refusedEvery(t, refusals,
		"graph.yaml.nodes[0].config.source: nodes/missing.js: the referenced file does not exist inside the content root")
}

// TestValidateRefusesReferencesThatLeaveTheContentRoot covers the ways a
// reference can point out of the package: a parent path, an absolute
// path, a directory and a symbolic link. All four are refused before
// anything is copied.
func TestValidateRefusesReferencesThatLeaveTheContentRoot(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	writeTestFile(t, filepath.Dir(outside), filepath.Base(outside), "name: outside\n")
	dir := newApp(t)
	writeTestFile(t, dir, "graph.yaml", `name: hello
entry: hello
nodes:
  - id: a
    type: script
    config:
      runtime: js
      source: { file: ../outside.yaml }
  - id: b
    type: script
    config:
      runtime: js
      source: { file: /etc/hosts }
  - id: c
    type: script
    config:
      runtime: js
      source: { file: nodes }
  - id: d
    type: script
    config:
      runtime: js
      source: { file: link.yaml }
edges:
  - { from: a, to: __end__ }
`)
	if err := os.Symlink(outside, filepath.Join(dir, "link.yaml")); err != nil {
		t.Fatal(err)
	}
	refusals := refusalsOf(t, validate(t, dir))
	refusedEvery(t, refusals,
		"../outside.yaml: the path must be relative to the content root",
		"/etc/hosts: the path must be relative to the content root",
		"nodes: the referenced file is a directory",
		"link.yaml: the referenced file is a symbolic link")
}

// TestValidateRefusesEmbedReferences: only the host's own layers are
// embedded content, and an application deployment has no embed FS.
func TestValidateRefusesEmbedReferences(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `agents:
  app:
    card:
      name: Hello
    engine:
      settings:
        graph: { embed: assets/graph.yaml }
`)
	refusals := refusalsOf(t, validate(t, dir))
	refusedEvery(t, refusals,
		"cannot reference embedded assets")
}

// TestValidateChecksWhatTheManifestPromised covers the files a manifest
// names: the layers, the frontend bundle and an icon path.
func TestValidateChecksWhatTheManifestPromised(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, ManifestFile, `app: v1
id: hello
name: Hello
version: 0.1.0
icon: icon.png
layers:
  - layer.yaml
  - missing.yaml
ui:
  entry: ui/dist/index.js
  style: ui/dist/index.css
`)
	refusals := refusalsOf(t, validate(t, dir))
	refusedEvery(t, refusals,
		"missing.yaml: the layer does not exist inside the content root",
		"icon.png: the icon does not exist inside the content root",
		"ui/dist/index.js: the frontend entry does not exist inside the content root",
		"ui/dist/index.css: the frontend stylesheet does not exist inside the content root")
}

// TestValidateAcceptsAnApplicationWithAFrontendAndAnIcon pins the other
// side of the checks above: a package that carries what it declares is
// accepted.
func TestValidateAcceptsAnApplicationWithAFrontendAndAnIcon(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, ManifestFile, `app: v1
id: hello
name: Hello
version: 0.1.0
icon: icon.png
layers:
  - layer.yaml
ui:
  entry: ui/dist/index.js
  style: ui/dist/index.css
defaults:
  model: test-model
  think_level: low
`)
	writeTestFile(t, dir, "icon.png", "not really a png, but a file")
	writeTestFile(t, dir, "ui/dist/index.js", "export function apply() {}\n")
	writeTestFile(t, dir, "ui/dist/index.css", ".hello { color: red }\n")
	if err := validate(t, dir); err != nil {
		t.Fatalf("the application was refused: %v", err)
	}
}

// TestValidateReadsEveryProblemAtOnce pins the shape of the verdict: a
// refused application gets the whole list — both passes, in a stable
// order — so a user fixing a foreign document sees all of it the first
// time.
func TestValidateReadsEveryProblemAtOnce(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", `resources:
  events:
    kind: event.Bus
  shell:
    kind: sandbox.Runner
  infer:
    kind: inference.Assembly
agents:
  app:
    card:
      name: Hello
`)
	first := refusalsOf(t, validate(t, dir))
	if len(first.List) != 4 {
		t.Fatalf("refusals = %d, want 4:\n%s", len(first.List), first.Error())
	}
	if first.App != "hello" {
		t.Errorf("refusals name app %q, want hello", first.App)
	}
	if !strings.HasPrefix(first.Error(), `apps: "hello" refused for 4 reasons:`) {
		t.Errorf("error text does not open the list:\n%s", first.Error())
	}
	if !strings.Contains(first.Error(), "\n  - layer.yaml: resources.events:") {
		t.Errorf("error text does not list one refusal per line:\n%s", first.Error())
	}
	// The three declaration refusals come first and the document pass's
	// refusal closes the list: the whole verdict is one ordered list,
	// not two reports.
	if !strings.Contains(first.Error(),
		"\n  - layer.yaml: agents.app.engine.settings.graph: the entry agent declares no graph") {
		t.Errorf("the list does not carry the document pass's refusal:\n%s", first.Error())
	}
	second := refusalsOf(t, validate(t, dir))
	if first.Error() != second.Error() {
		t.Errorf("two runs refused differently:\n%s\n%s", first.Error(), second.Error())
	}
}

// TestValidateRefusesALayerThatDoesNotParse keeps a bad layer's own
// error: the message the core decoder produced names the key and the
// line, and rewriting it would throw away the only thing that helps.
func TestValidateRefusesALayerThatDoesNotParse(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, "layer.yaml", "agents: [not, a, mapping]\n")
	refusals := refusalsOf(t, validate(t, dir))
	refusedEvery(t, refusals,
		"layer.yaml: the layer is not a deployment document",
		"agents")
}

// TestValidateStopsAtALayerThePackageDoesNotCarry: when a manifest names
// a file the package does not carry, that is the whole verdict. The
// layers that are left are not the package the manifest describes, so
// reading them would report refusals about a document nobody wrote —
// next to a file the author has to add before any of that is read again.
func TestValidateStopsAtALayerThePackageDoesNotCarry(t *testing.T) {
	dir := newApp(t)
	writeTestFile(t, dir, ManifestFile, `app: v1
id: hello
name: Hello
version: 0.1.0
layers:
  - layer.yaml
  - graph-layer.yaml
`)
	// A real problem in the layer that is there, and deliberately not
	// reported while the package is incomplete.
	writeTestFile(t, dir, "layer.yaml", `resources:
  events:
    kind: event.Bus
`)
	refusals := refusalsOf(t, validate(t, dir))
	if len(refusals.List) != 1 {
		t.Fatalf("refusals = %d, want 1:\n%s", len(refusals.List), refusals.Error())
	}
	refusedEvery(t, refusals,
		"graph-layer.yaml: the layer does not exist inside the content root")
}
