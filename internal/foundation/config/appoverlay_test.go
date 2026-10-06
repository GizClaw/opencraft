package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"
)

// TestAppendInferenceOverlayMovesOnlyTheInferenceWiring pins the one
// property the whole overlay rests on: the key set. Everything the
// settings page generates moves (the router, the infer assembly, every
// provider.*), and nothing else does — not the sandbox override a
// seeded user layer carries, not the agents a user added, not the
// version of the layer the overlay is merged over.
func TestAppendInferenceOverlayMovesOnlyTheInferenceWiring(t *testing.T) {
	src := deploy.Document{
		Version: "v9",
		Resources: resource.Resources{
			"router": {
				Kind: "inference.Router",
				Impl: "unified",
				Settings: json.RawMessage(
					`{"generate":[{"targets":["provider.deepseek"]}]}`),
			},
			"infer": {
				Kind: "inference.Assembly",
				Impl: "unified",
			},
			"provider.deepseek": {
				Kind: "inference.Provider",
				Impl: "openai",
				Settings: json.RawMessage(
					`{"api_key":"${secret:keychain.deepseek}"}`),
			},
			"box": {
				Kind: "sandbox.Runner",
				Impl: "local",
			},
			// The tool knobs live in the user layer too and are keyed by
			// deployment id, so a provider-shaped name that is not one
			// must not travel: only the provider.* prefix does.
			"tools.assistant": {Kind: "tool.Registry", Impl: "memory"},
		},
		Agents: map[string]agent.Definition{"custom": {}},
	}
	var dst deploy.Document
	dst.Version = "v1"
	if !AppendInferenceOverlay(&dst, src) {
		t.Fatal("AppendInferenceOverlay moved nothing, want the inference wiring")
	}
	want := []string{"infer", "provider.deepseek", "router"}
	if len(dst.Resources) != len(want) {
		t.Fatalf("moved resources = %v, want %v", keysOf(dst.Resources), want)
	}
	for _, key := range want {
		if _, ok := dst.Resources[key]; !ok {
			t.Errorf("resource %q did not move (moved: %v)", key, keysOf(dst.Resources))
		}
	}
	if got := string(dst.Resources["provider.deepseek"].Settings); !strings.Contains(got, "${secret:keychain.deepseek}") {
		t.Errorf("provider settings did not travel verbatim: %s", got)
	}
	if dst.Version != "v1" {
		t.Errorf("version = %q, want the destination's own (v1)", dst.Version)
	}
	if len(dst.Agents) != 0 {
		t.Errorf("agents moved: %v", dst.Agents)
	}

	// A layer with nothing to contribute reports so instead of
	// publishing an empty overlay.
	var empty deploy.Document
	if AppendInferenceOverlay(&empty, deploy.Document{}) {
		t.Error("AppendInferenceOverlay({}) = true, want false")
	}
	if len(empty.Resources) != 0 {
		t.Errorf("empty overlay grew resources: %v", keysOf(empty.Resources))
	}
}

func keysOf(res resource.Resources) []string {
	keys := make([]string, 0, len(res))
	for key := range res {
		keys = append(keys, key)
	}
	return keys
}

func writeUserLayer(t *testing.T, data string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "opencraft.yaml"), []byte(data), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestUserInferenceOverlayRendersAPartialInferenceDocument pins what
// the app assembly receives: a partial document (no version, no
// agents) carrying exactly the inference resources, with the settings
// — secret references included — byte-for-byte as the user layer wrote
// them. The sandbox override in the same file is what must not leak
// into an application deployment.
func TestUserInferenceOverlayRendersAPartialInferenceDocument(t *testing.T) {
	dir := writeUserLayer(t, `version: v1
resources:
  box:
    settings:
      remote: false
  router:
    kind: inference.Router
    impl: unified
    settings:
      generate:
        - targets: [provider.deepseek]
  infer:
    kind: inference.Assembly
    impl: unified
  provider.deepseek:
    kind: inference.Provider
    impl: openai
    settings:
      api_key: ${secret:keychain.deepseek}
agents:
  custom:
    card: { name: Custom }
`)
	blob, ok, err := UserInferenceOverlay(dir)
	if err != nil {
		t.Fatalf("UserInferenceOverlay: %v", err)
	}
	if !ok {
		t.Fatal("UserInferenceOverlay found nothing, want the inference wiring")
	}
	var doc deploy.Document
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatalf("overlay is not a deploy document: %v\n%s", err, blob)
	}
	if doc.Version != "" {
		t.Errorf("overlay carries version %q, want a partial document", doc.Version)
	}
	if len(doc.Agents) != 0 {
		t.Errorf("overlay carries agents: %v", doc.Agents)
	}
	want := []string{"infer", "provider.deepseek", "router"}
	if len(doc.Resources) != len(want) {
		t.Fatalf("overlay resources = %v, want %v", keysOf(doc.Resources), want)
	}
	for _, key := range want {
		if _, ok := doc.Resources[key]; !ok {
			t.Errorf("overlay is missing %q (has %v)", key, keysOf(doc.Resources))
		}
	}
	got := string(doc.Resources["provider.deepseek"].Settings)
	if !strings.Contains(got, "${secret:keychain.deepseek}") {
		t.Errorf("provider settings lost the secret reference: %s", got)
	}
}

// TestUserInferenceOverlayReportsTheUnconfiguredStates pins the three
// ways "there is no overlay" is expressed — absent file, file without
// inference wiring, and a versionless hand-written layer (partial
// semantics: the overlay is never the lowest-priority layer, so a
// missing version is not its business).
func TestUserInferenceOverlayReportsTheUnconfiguredStates(t *testing.T) {
	if blob, ok, err := UserInferenceOverlay(t.TempDir()); err != nil || ok {
		t.Errorf("absent user layer: (%s, %v, %v), want (nil, false, nil)",
			blob, ok, err)
	}
	dir := writeUserLayer(t, "version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if _, ok, err := UserInferenceOverlay(dir); err != nil || ok {
		t.Errorf("user layer without inference: ok=%v err=%v, want false/nil", ok, err)
	}
	dir = writeUserLayer(t, "resources:\n  infer:\n    kind: inference.Assembly\n    impl: unified\n")
	blob, ok, err := UserInferenceOverlay(dir)
	if err != nil || !ok {
		t.Fatalf("versionless user layer: ok=%v err=%v, want true/nil", ok, err)
	}
	var doc deploy.Document
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatalf("overlay is not a deploy document: %v", err)
	}
	if _, ok := doc.Resources["infer"]; !ok {
		t.Errorf("versionless layer lost the infer resource: %v", keysOf(doc.Resources))
	}
}

// TestUserInferenceOverlayRewritesALegacyUserLayer keeps the legacy
// shape migration on the app path: the overlay is rendered from the
// rewritten document, so a model key the drivers now reject cannot
// travel into an application deployment, where it would surface as a
// strict-decode failure inside a provider, thousands of lines away from
// the file that carried it.
func TestUserInferenceOverlayRewritesALegacyUserLayer(t *testing.T) {
	cfg := envKeyed(t, "openai")
	data, err := cfg.InferenceYAML()
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(
		string(data),
		"kind: 'generate'",
		"kind: 'generate'\n"+
			"            effort_none: true\n"+
			"            responses: true",
		1,
	)
	dir := writeUserLayer(t, stale)
	blob, ok, err := UserInferenceOverlay(dir)
	if err != nil {
		t.Fatalf("UserInferenceOverlay: %v", err)
	}
	if !ok {
		t.Fatal("UserInferenceOverlay found nothing, want the inference wiring")
	}
	for _, key := range []string{"effort_none", "responses: true"} {
		if strings.Contains(string(blob), key) {
			t.Errorf("overlay carries the retired key %q: %s", key, blob)
		}
	}
	// What it does carry is the canonical wiring, not a truncated
	// document: the rewrite is where the deprecated keys go, not where
	// the layer's inference does.
	var doc deploy.Document
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatalf("overlay is not a deploy document: %v", err)
	}
	for _, key := range []string{"router", "infer"} {
		if _, ok := doc.Resources[key]; !ok {
			t.Errorf("overlay is missing %q: %v", key, keysOf(doc.Resources))
		}
	}
	providers := 0
	for key := range doc.Resources {
		if strings.HasPrefix(key, "provider.") {
			providers++
		}
	}
	if providers == 0 {
		t.Errorf("overlay lost every provider: %v", keysOf(doc.Resources))
	}
}

// TestUserInferenceOverlayRefusesRetiredRefs keeps the upgrade
// diagnostic on the app path: a user layer that still references a
// retired ${env:OPEN_CRAFT_*} variable fails here, naming the file and
// the replacement, instead of surfacing inside deployment as an unset
// environment variable.
func TestUserInferenceOverlayRefusesRetiredRefs(t *testing.T) {
	dir := writeUserLayer(t, `version: v1
resources:
  infer:
    kind: inference.Assembly
    impl: unified
    settings:
      work_dir: ${env:OPEN_CRAFT_WORKDIR}
`)
	_, ok, err := UserInferenceOverlay(dir)
	if ok || err == nil {
		t.Fatalf("ok=%v err=%v, want the retired-reference error", ok, err)
	}
	for _, want := range []string{"opencraft.yaml", "OPEN_CRAFT_WORKDIR", "${ocraft:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
