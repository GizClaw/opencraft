package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/utils"
)

// capabilityDoc decodes one embedded fragment layer, so a test reads the
// file the assembly reads.
func capabilityDoc(t *testing.T, asset string) deploy.Document {
	t.Helper()
	raw, err := FS().ReadFile(asset)
	if err != nil {
		t.Fatalf("read %s: %v", asset, err)
	}
	doc, err := utils.Decode[deploy.Document](raw)
	if err != nil {
		t.Fatalf("decode %s: %v", asset, err)
	}
	return doc
}

// toolSources lists the tool containers one decoded fragment declares.
func toolSources(doc deploy.Document) []string {
	out := make([]string, 0, len(doc.Resources))
	for key, res := range doc.Resources {
		if res.Kind == "tool.Source" {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// containers lists the containers one decoded fragment contributes to
// the shared tool assembly.
func containers(t *testing.T, doc deploy.Document) []string {
	t.Helper()
	assembly, ok := doc.Resources[CapabilityToolsKey]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(assembly.Deps))
	for key, ref := range assembly.Deps {
		if key != string(ref) {
			t.Fatalf("container %q contributes %q; a source's dep is its own key",
				key, ref)
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// TestCapabilityTableMatchesTheEmbeddedFragments pins the table against
// the files it names: every capability has a layer, every layer belongs
// to a capability (an orphan file would be a fragment nothing can turn
// on), and each fragment's contribution to the shared assembly is
// exactly the containers it declares — the list below is the content of
// the fragment, so widening one is a deliberate test change.
func TestCapabilityTableMatchesTheEmbeddedFragments(t *testing.T) {
	entries, err := FS().ReadDir("assets/capabilities")
	if err != nil {
		t.Fatalf("read the fragment directory: %v", err)
	}
	onDisk := make([]string, 0, len(entries))
	for _, entry := range entries {
		onDisk = append(onDisk, entry.Name())
	}
	want := make([]string, 0, len(capabilityTable)+1)
	for _, capability := range capabilityTable {
		want = append(want, capability+".yaml")
	}
	// assembly.yaml is the shared policy, not a capability: it is the
	// one file in the directory no capability is named after.
	want = append(want, "assembly.yaml")
	sort.Strings(want)
	sort.Strings(onDisk)
	if !reflect.DeepEqual(onDisk, want) {
		t.Errorf("embedded fragments = %v, want %v", onDisk, want)
	}

	wantContainers := map[string][]string{
		"tools": {"tool.applypatch", "tool.files", "tool.viewimage"},
		"exec":  {"tool.exec"},
		"web":   {"tool.webfetch", "tool.websearch"},
	}
	if len(wantContainers) != len(capabilityTable) {
		t.Fatalf("the expectation table covers %d capabilities, the fragment table %d",
			len(wantContainers), len(capabilityTable))
	}
	for _, capability := range capabilityTable {
		doc := capabilityDoc(t, capabilityAsset(capability))
		declared := toolSources(doc)
		if !reflect.DeepEqual(declared, wantContainers[capability]) {
			t.Errorf("capability %q declares containers %v, want %v",
				capability, declared, wantContainers[capability])
		}
		if got := containers(t, doc); !reflect.DeepEqual(got, declared) {
			t.Errorf("capability %q contributes %v, want what it declares (%v)",
				capability, got, declared)
		}
	}
}

// TestCapabilityFragmentsAgreeOnSharedResources: a resource two
// fragments declare is one policy with two writers, and the merge keeps
// whichever layer is higher — so the two must not drift. Deps are the
// exception the fragment idea is built on (each contributes its own
// containers to the shared assembly); everything else has to match.
func TestCapabilityFragmentsAgreeOnSharedResources(t *testing.T) {
	docs := make(map[string]deploy.Document, len(capabilityTable))
	var names []string
	for _, capability := range CapabilityNames() {
		docs[capability] = capabilityDoc(t, capabilityAsset(capability))
		names = append(names, capability)
	}
	for i, left := range names {
		for _, right := range names[i+1:] {
			for key, leftRes := range docs[left].Resources {
				rightRes, shared := docs[right].Resources[key]
				if !shared {
					continue
				}
				if leftRes.Kind != rightRes.Kind || leftRes.Impl != rightRes.Impl {
					t.Errorf("%q and %q declare %q as %s/%s and %s/%s",
						left, right, key, leftRes.Kind, leftRes.Impl,
						rightRes.Kind, rightRes.Impl)
				}
				if !sameJSON(leftRes.Settings, rightRes.Settings) {
					t.Errorf("%q and %q declare different %s settings:\n%s\n%s",
						left, right, key, leftRes.Settings, rightRes.Settings)
				}
			}
		}
	}
}

// TestCapabilityAssemblySharesTheAssistantPolicy: the tool result chain
// an application gets is the assistant's own (redact, truncate, audit,
// result limit), declared a second time because the assistant's document
// is not part of an application deployment. Every middleware both of
// them configure has to agree, and the application's chain deliberately
// has no dynamic discovery policy (the application's own graph decides
// what the model sees).
func TestCapabilityAssemblySharesTheAssistantPolicy(t *testing.T) {
	assistant := middlewareSettingsOf(t, "assets/tools.yaml")
	app := middlewareSettingsOf(t, capabilityAssemblyAsset)
	if len(app) == 0 {
		t.Fatalf("%s declares no middlewares", capabilityAssemblyAsset)
	}
	for key, want := range app {
		got, shared := assistant[key]
		if !shared {
			t.Errorf("the application chain declares %q, the assistant's does not", key)
			continue
		}
		if !sameJSON(want, got) {
			t.Errorf("middleware %q differs:\napplication: %s\nassistant:   %s",
				key, want, got)
		}
	}
	for key := range assistant {
		if _, ok := app[key]; !ok {
			t.Logf("the assistant configures %q; the application chain does not", key)
		}
	}
	if _, ok := app["dynamic"]; ok {
		t.Errorf("the application chain declares a dynamic policy; " +
			"an application's graph decides what its model sees")
	}
}

// middlewareSettingsOf reads one layer's tool assembly middleware
// settings, keeping them as raw JSON so the comparison is on the
// document rather than on one Go shape of it.
func middlewareSettingsOf(t *testing.T, asset string) map[string]json.RawMessage {
	t.Helper()
	doc := capabilityDoc(t, asset)
	assembly, ok := doc.Resources[CapabilityToolsKey]
	if !ok {
		t.Fatalf("%s declares no %q resource", asset, CapabilityToolsKey)
	}
	var settings struct {
		Middlewares map[string]json.RawMessage `json:"middlewares"`
	}
	if err := json.Unmarshal(assembly.Settings, &settings); err != nil {
		t.Fatalf("%s: decode assembly settings: %v", asset, err)
	}
	return settings.Middlewares
}

// sameJSON compares two JSON values by their decoded form.
func sameJSON(left, right json.RawMessage) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	var leftValue, rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// writeLayer writes one partial application layer into a fresh content
// root, so a test can assemble a real stack.
func writeLayer(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAppDeployLayersComposeTheHostBands builds the stack the way both
// callers do and reads the merged document: the contract layer's own
// resources stay, the fragments' resources arrive, the containers of
// every enabled fragment land in one assembly, and every agent the
// application runs is wired to it — that last one is the whole point of
// the generated layer, because a dependency is per agent.
func TestAppDeployLayersComposeTheHostBands(t *testing.T) {
	dir := t.TempDir()
	writeLayer(t, dir, "one.yaml", `agents:
  app:
    card:
      name: Hello
    engine:
      settings:
        graph: { file: graph.yaml }
`)
	writeLayer(t, dir, "two.yaml", `agents:
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
        tools: tools
      settings:
        graph: { file: judge.yaml }
`)
	stack, err := AppDeployLayers(
		dir, []string{"one.yaml", "two.yaml"},
		[]string{"tools", "exec"}, []string{"app", "judge"})
	if err != nil {
		t.Fatalf("compose the stack: %v", err)
	}
	priorities := make([]int, 0, len(stack))
	for _, layer := range stack {
		if len(priorities) > 0 && layer.Priority <= priorities[len(priorities)-1] {
			t.Errorf("layer %q at priority %d does not follow %d",
				layer.Name, layer.Priority, priorities[len(priorities)-1])
		}
		priorities = append(priorities, layer.Priority)
	}

	doc, _, err := deploy.LoadLayers(context.Background(), stack)
	if err != nil {
		t.Fatalf("load the stack: %v", err)
	}
	for _, key := range []string{"sessions", "ws", "hostws", "tool.files", "box", "execpolicy", "tool.exec"} {
		if _, ok := doc.Resources[key]; !ok {
			t.Errorf("the merged document declares no %q", key)
		}
	}
	assembly, ok := doc.Resources[CapabilityToolsKey]
	if !ok {
		t.Fatalf("the merged document declares no %q", CapabilityToolsKey)
	}
	if assembly.Kind != "tool.Assembly" || assembly.Impl != "opencraft" {
		t.Errorf("the assembly is %s/%s, want tool.Assembly/opencraft",
			assembly.Kind, assembly.Impl)
	}
	want := map[string]string{
		"tool.files": "tool.files", "tool.applypatch": "tool.applypatch",
		"tool.viewimage": "tool.viewimage", "tool.exec": "tool.exec",
	}
	got := make(map[string]string, len(assembly.Deps))
	for key, ref := range assembly.Deps {
		got[key] = string(ref)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("assembly deps = %v, want %v", got, want)
	}
	if len(assembly.Settings) == 0 {
		t.Error("the assembly carries no settings: the shared policy layer did not merge")
	}
	for _, name := range []string{"app", "judge"} {
		def, ok := doc.Agents[name]
		if !ok {
			t.Fatalf("the merged document declares no agent %q", name)
		}
		if got := string(def.Engine.Deps[CapabilityToolsKey]); got != CapabilityToolsKey {
			t.Errorf("agent %q is wired to %q, want %q", name, got, CapabilityToolsKey)
		}
		if def.Engine.Kind == "" {
			t.Errorf("agent %q lost its engine kind in the merge", name)
		}
	}
}

// TestAppDeployLayersWithoutCapabilitiesIsJustTheApplication: the host
// bands are not a second contract — an application that opts into
// nothing gets the contract layer and its own layers, and no assembly to
// reach.
func TestAppDeployLayersWithoutCapabilitiesIsJustTheApplication(t *testing.T) {
	dir := t.TempDir()
	writeLayer(t, dir, "one.yaml", "agents:\n  app:\n    card:\n      name: Hello\n")
	stack, err := AppDeployLayers(dir, []string{"one.yaml"}, nil, nil)
	if err != nil {
		t.Fatalf("compose the stack: %v", err)
	}
	if len(stack) != 2 {
		t.Fatalf("stack = %d layers, want the contract layer and the application's one",
			len(stack))
	}
	doc, _, err := deploy.LoadLayers(context.Background(), stack)
	if err != nil {
		t.Fatalf("load the stack: %v", err)
	}
	if _, ok := doc.Resources[CapabilityToolsKey]; ok {
		t.Errorf("an application without capabilities got a %q resource", CapabilityToolsKey)
	}
	if got := string(doc.Agents["app"].Engine.Deps[CapabilityToolsKey]); got != "" {
		t.Errorf("an application without capabilities is wired to %q", got)
	}
}

// TestAppDeployLayersRefusesWhatItCannotWire: a name this build does not
// ship, a capability with no agent to reach, and a layer name that walks
// out of the content root are the three ways the stack cannot be honest
// about itself.
func TestAppDeployLayersRefusesWhatItCannotWire(t *testing.T) {
	dir := t.TempDir()
	writeLayer(t, dir, "one.yaml", "agents:\n  app:\n    card:\n      name: Hello\n")
	cases := []struct {
		name         string
		layers       []string
		capabilities []string
		agents       []string
		want         string
	}{
		{
			name: "unknown capability", layers: []string{"one.yaml"},
			capabilities: []string{"sudo"}, agents: []string{"app"},
			want: `"sudo" is not a capability this build provides (want exec, tools, web)`,
		},
		{
			name: "no agent to wire", layers: []string{"one.yaml"},
			capabilities: []string{"tools"}, agents: nil,
			want: "no agent to wire them into",
		},
		{
			name: "empty agent name", layers: []string{"one.yaml"},
			capabilities: []string{"tools"}, agents: []string{""},
			want: "agent name is empty",
		},
		{
			name: "layer outside the root", layers: []string{"../one.yaml"},
			capabilities: []string{"tools"}, agents: []string{"app"},
			want: "is not a relative path inside the content root",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AppDeployLayers(dir, tc.layers, tc.capabilities, tc.agents)
			if err == nil {
				t.Fatal("the stack composed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to read %q", err, tc.want)
			}
		})
	}
}

// TestAppDeployLayersWiringCarriesNoZeroValues: the wiring is generated
// JSON, and a zero value that leaked into it would silently rewrite an
// agent the application declared (an empty card name, an empty engine
// kind). Only the dependency is in there.
func TestAppDeployLayersWiringCarriesNoZeroValues(t *testing.T) {
	dir := t.TempDir()
	writeLayer(t, dir, "one.yaml", `agents:
  app:
    card:
      name: Hello
      description: mine
    engine:
      settings:
        graph: { file: graph.yaml }
`)
	stack, err := AppDeployLayers(dir, []string{"one.yaml"}, []string{"web"}, []string{"app"})
	if err != nil {
		t.Fatalf("compose the stack: %v", err)
	}
	doc, _, err := deploy.LoadLayers(context.Background(), stack)
	if err != nil {
		t.Fatalf("load the stack: %v", err)
	}
	definition := doc.Agents["app"]
	if definition.Card.Name != "Hello" || definition.Card.Description != "mine" {
		t.Errorf("the wiring rewrote the card: %+v", definition.Card)
	}
	var settings map[string]any
	if err := json.Unmarshal(definition.Engine.Settings, &settings); err != nil {
		t.Fatalf("decode the merged engine settings: %v", err)
	}
	if _, ok := settings["graph"]; !ok {
		t.Errorf("the wiring dropped the graph reference: %s", definition.Engine.Settings)
	}
}

// TestAppLayerNameAnswersOnlyForTheApplicationBand: a refusal may only
// claim an application file declared a key when a layer in the
// application band did.
func TestAppLayerNameAnswersOnlyForTheApplicationBand(t *testing.T) {
	cases := []struct {
		ref  deploy.LayerRef
		want string
	}{
		{deploy.LayerRef{Priority: 0, Name: "contract"}, ""},
		{deploy.LayerRef{Priority: AppLayerPriorityBase, Name: "layer.yaml"}, "layer.yaml"},
		{deploy.LayerRef{Priority: AppLayerPriorityBase + 3, Name: "second.yaml"}, "second.yaml"},
		{deploy.LayerRef{Priority: AppCapabilityPriorityBase, Name: "capabilities"}, ""},
		{deploy.LayerRef{Priority: AppCapabilityPriorityBase + 1, Name: "capability:exec"}, ""},
		{deploy.LayerRef{Priority: AppWiringPriorityBase, Name: "capability-wiring"}, ""},
		{deploy.LayerRef{Priority: AppOverlayPriorityBase, Name: "inference"}, ""},
		{deploy.LayerRef{}, ""},
	}
	for _, tc := range cases {
		if got := AppLayerName(tc.ref); got != tc.want {
			t.Errorf("AppLayerName(%+v) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}
