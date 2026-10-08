package apps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// The capability list is the manifest's half of the fragment mechanism:
// the host owns the layers (foundation/config), the manifest owns the
// request, and neither can drift because the names are checked against
// the host's table when the file is read.

// withManifest rewrites the fixture manifest with extra lines appended,
// so a test changes exactly what it is about.
func withManifest(t *testing.T, dir, extra string) {
	t.Helper()
	writeTestFile(t, dir, ManifestFile, fixtureManifest+extra)
}

// withLayer rewrites the fixture's deployment layer, which is where an
// application declares what its agents depend on.
func withLayer(t *testing.T, dir, body string) {
	t.Helper()
	writeTestFile(t, dir, "layer.yaml", body)
}

// TestManifestReadsTheCapabilityList: the names reach the value the
// preflight and the assembly read (App.Capabilities), normalized, so a
// layer band and a host band cannot disagree about what was asked for.
func TestManifestReadsTheCapabilityList(t *testing.T) {
	dir := newApp(t)
	withManifest(t, dir, "capabilities:\n  - \" tools \"\n  - exec\n")
	app := appFor(t, dir)
	if got := strings.Join(app.Capabilities, ","); got != "tools,exec" {
		t.Errorf("capabilities = %q, want tools,exec", got)
	}
	if err := validate(t, dir); err != nil {
		t.Fatalf("an application that opts into fragments was refused: %v", err)
	}
}

// TestPreflightWalksOnlyWhatTheApplicationDeclared: the merged
// document's settings are walked for references so an application's
// `{file:}` stays inside the content root — but the host bands above the
// application (the contract layer's own keys, the capability fragments)
// are the host's embedded layers. Their references are not the
// application's to fix, and a refusal has no application file to name,
// so the walk has to leave them alone. The document below is the shape
// that would otherwise refuse: a fragment's setting carrying an
// embedded asset.
func TestPreflightWalksOnlyWhatTheApplicationDeclared(t *testing.T) {
	p := &preflight{
		app:  App{ID: "hello", Agent: DefaultAgent},
		root: t.TempDir(),
		provenance: deploy.Provenance{
			Agents: map[string]deploy.LayerRef{
				DefaultAgent: {Priority: config.AppLayerPriorityBase, Name: "layer.yaml"},
			},
			Resources: map[string]deploy.LayerRef{
				"tool.files": {Priority: config.AppCapabilityPriorityBase + 1, Name: "capability:tools"},
			},
		},
	}
	p.document(deploy.Document{
		Agents: map[string]agent.Definition{
			DefaultAgent: {Engine: agent.EngineRef{
				Kind:     "agent.Engine",
				Impl:     "graph",
				Settings: json.RawMessage(`{"graph": "name: x\nentry: world\nnodes: []"}`),
			}},
		},
		Resources: resource.Resources{
			"tool.files": {Kind: "tool.Source", Impl: "opencraft/files",
				Settings: json.RawMessage(`{"prompt": {"embed": "assets/prompt.md"}}`)},
		},
	})
	if len(p.refusals.List) != 0 {
		t.Errorf("the preflight refused the host band's own references:\n%s", p.refusals.Error())
	}
}

// TestManifestRefusesAnUnknownCapability: a name the host does not ship
// is refused with the ones it does — the fragment an author believes
// they enabled is the one their graph is written against, so a silent
// no-op would surface as a tool the model cannot call.
func TestManifestRefusesAnUnknownCapability(t *testing.T) {
	dir := newApp(t)
	withManifest(t, dir, "capabilities:\n  - sudo\n")
	_, err := ParseManifest([]byte(fixtureManifest + "capabilities:\n  - sudo\n"))
	if err == nil {
		t.Fatal("the manifest was accepted")
	}
	want := `capability "sudo" is not one this host provides (want ` +
		strings.Join(config.CapabilityNames(), ", ") + `)`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to read %q", err, want)
	}
	if _, err := readManifest(t, dir); err == nil {
		t.Error("reading the manifest off disk accepted the unknown capability")
	}
}

// TestManifestRefusesCapabilityListsItCannotRead walks the shapes a
// capability list must not have: a repeated name (which would put two
// fragment layers in the same band), an empty entry, and a list longer
// than the bound.
func TestManifestRefusesCapabilityListsItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		list string
		want string
	}{
		{
			name: "repeated",
			list: "capabilities:\n  - tools\n  - tools\n",
			want: `capability "tools" is listed twice`,
		},
		{
			name: "empty",
			list: "capabilities:\n  - \"  \"\n",
			want: "capabilities[0] is empty",
		},
		{
			name: "too many",
			list: "capabilities:\n" + strings.Repeat("  - tools\n", maxCapabilityCount+1),
			want: "capabilities exceeds the 8 an application may declare",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(fixtureManifest + tc.list))
			if err == nil {
				t.Fatal("the manifest was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to read %q", err, tc.want)
			}
		})
	}
}

// TestTheApplicationBandCannotReachTheCapabilityBand: the manifest's
// layer bound and the host's priority bands are numbers in two packages
// that have to stay apart. An application layer at 100+ would be read as
// a host band by every provenance check (config.AppLayerName), and the
// promise that a fragment cannot be shadowed would rest on the preflight
// noticing instead of on the merge. Raising maxLayerCount without moving
// AppCapabilityPriorityBase is how this breaks.
func TestTheApplicationBandCannotReachTheCapabilityBand(t *testing.T) {
	last := config.AppLayerPriorityBase + maxLayerCount - 1
	if last >= config.AppCapabilityPriorityBase {
		t.Errorf("the %d layers a manifest may declare reach priority %d, "+
			"into the capability band that starts at %d: an application "+
			"layer would be read as a host band",
			maxLayerCount, last, config.AppCapabilityPriorityBase)
	}
}

// readManifest reads the manifest of a written fixture off disk the way
// the store does.
func readManifest(t *testing.T, dir string) (*Manifest, error) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	return ParseManifest(raw)
}

// TestValidateAcceptsTheToolsFragment: with the fragment enabled, the
// document pass reads the merged document — the fragment's resources are
// in it — so an agent that depends on the tool assembly names something
// that exists. This is the pair that makes the opt-in real: the same
// layer is refused without the capability (see below).
func TestValidateAcceptsTheToolsFragment(t *testing.T) {
	dir := newApp(t)
	withManifest(t, dir, "capabilities:\n  - tools\n")
	withLayer(t, dir, `agents:
  app:
    card:
      name: Hello
    engine:
      deps:
        tools: tools
      settings:
        graph: { file: graph.yaml }
`)
	if err := validate(t, dir); err != nil {
		t.Fatalf("an application that opted into tools was refused: %v", err)
	}
}

// TestValidateRefusesAToolsDependencyWithoutTheCapability: the same
// layer without the capability names a resource no layer declares. The
// refusal has to say so rather than let the assembly fail later with a
// message about the graph engine.
func TestValidateRefusesAToolsDependencyWithoutTheCapability(t *testing.T) {
	dir := newApp(t)
	withLayer(t, dir, `agents:
  app:
    card:
      name: Hello
    engine:
      deps:
        tools: tools
      settings:
        graph: { file: graph.yaml }
`)
	refusals := refusalsOf(t, validate(t, dir))
	refusedEvery(t, refusals,
		`layer.yaml: agents.app.engine.deps.tools: "tools" names no resource in the merged document`)
}

// TestValidateRefusesAnApplicationLayerClaimingTheToolAssembly: the tool
// assembly's key belongs to the host band, which sits above the
// application's layers — a layer that declared it would be shadowed and
// silently lose. The key is only the host's once the application opts
// into a capability: without one there is no assembly to claim, and the
// layer is judged by the kind table like any other.
func TestValidateRefusesAnApplicationLayerClaimingTheToolAssembly(t *testing.T) {
	layer := `resources:
  tools:
    kind: agent.ScriptRuntime
    impl: js
agents:
  app:
    card:
      name: Hello
    engine:
      settings:
        graph: { file: graph.yaml }
`
	with := newApp(t)
	withManifest(t, with, "capabilities:\n  - web\n")
	withLayer(t, with, layer)
	refusals := refusalsOf(t, validate(t, with))
	refusedEvery(t, refusals, `layer.yaml: resources.tools: the host declares "tools" `+
		`once an application opts into a capability`)

	without := newApp(t)
	withLayer(t, without, layer)
	if err := validate(t, without); err != nil {
		t.Fatalf("without a capability the key is the application's to use: %v", err)
	}
}

// TestValidateReadsTheFragmentsTheManifestNames: two fragments enabled
// at once merge their containers into one assembly and their shared
// policy into one resource — and one fragment alone is enough for the
// deployment to be valid (a fragment that needed a sibling would make
// the list an order rather than a set).
func TestValidateReadsTheFragmentsTheManifestNames(t *testing.T) {
	for _, list := range []string{"tools", "exec", "web", "tools\n  - web"} {
		dir := newApp(t)
		withManifest(t, dir, "capabilities:\n  - "+list+"\n")
		withLayer(t, dir, `agents:
  app:
    card:
      name: Hello
    engine:
      deps:
        tools: tools
      settings:
        graph: { file: graph.yaml }
`)
		if err := validate(t, dir); err != nil {
			t.Errorf("capabilities [%s] were refused: %v",
				strings.ReplaceAll(list, "\n  - ", ", "), err)
		}
	}
}
