package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	"sigs.k8s.io/yaml"
)

// testGraph is a minimal graph definition used across lifecycle
// tests. The fake registrar does not compile graphs, so any parseable
// JSON/YAML suffices here.
const testGraph = `{"name":"sub","entry":"llm","nodes":[{"id":"llm","type":"inference","config":{"system_prompt":"SP","tool_pending_key":"tool_pending"}}],"edges":[{"from":"llm","to":"__end__","condition":"tool_pending == false"}]}`

// fakeRegistrar records registration/removal calls so tests can assert
// the lifecycle drives the runtime correctly.
type fakeRegistrar struct {
	mu            sync.Mutex
	registered    map[string]agent.Definition
	removed       []string
	registerErr   error
	registerFails int
	unregisterErr error
}

func newFakeRegistrar() *fakeRegistrar {
	return &fakeRegistrar{registered: make(map[string]agent.Definition)}
}

func (f *fakeRegistrar) RegisterAgent(
	_ context.Context,
	name string,
	def agent.Definition,
	_ ...runtimecore.RegisterAgentOption,
) (*agent.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.registerErr != nil {
		return nil, f.registerErr
	}
	if f.registerFails > 0 {
		f.registerFails--
		return nil, errdefs.Internalf("agent %q register failed (injected)", name)
	}
	if _, ok := f.registered[name]; ok {
		return nil, errdefs.Conflictf("agent %q already registered", name)
	}
	f.registered[name] = def
	return &agent.Agent{ID: name, Card: def.Card}, nil
}

func (f *fakeRegistrar) UnregisterAgent(
	_ context.Context,
	name string,
	_ ...runtimecore.UnregisterAgentOption,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unregisterErr != nil {
		return f.unregisterErr
	}
	delete(f.registered, name)
	f.removed = append(f.removed, name)
	return nil
}

func (f *fakeRegistrar) definitions() map[string]agent.Definition {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]agent.Definition, len(f.registered))
	for name, def := range f.registered {
		out[name] = def
	}
	return out
}

func newTestLifecycle(t *testing.T, reg registrar) (*Lifecycle, string) {
	t.Helper()
	dir := t.TempDir()
	lc, err := New(dir, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if reg != nil {
		lc.Bind(reg)
	}
	return lc, dir
}

func graphObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var graph map[string]any
	if err := yaml.Unmarshal([]byte(text), &graph); err != nil {
		t.Fatalf("decode graph %q: %v", text, err)
	}
	return graph
}

func assertGraphMatches(t *testing.T, got any, wantText string) {
	t.Helper()
	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("graph = %T, want map", got)
	}
	if !reflect.DeepEqual(gotMap, graphObject(t, wantText)) {
		t.Errorf("graph = %v, want %s", gotMap, wantText)
	}
}

func mustGraphText(t *testing.T, spec AgentSpec) string {
	t.Helper()
	text, err := spec.GraphText()
	if err != nil {
		t.Fatalf("GraphText: %v", err)
	}
	return text
}

// findNumber walks a generic json.Number tree (decoder.UseNumber) and
// returns the first value stored under key.
func findNumber(v any, key string) (json.Number, bool) {
	switch node := v.(type) {
	case map[string]any:
		if n, ok := node[key].(json.Number); ok {
			return n, true
		}
		for _, child := range node {
			if n, ok := findNumber(child, key); ok {
				return n, true
			}
		}
	case []any:
		for _, child := range node {
			if n, ok := findNumber(child, key); ok {
				return n, true
			}
		}
	}
	return "", false
}

func assertGraphNumber(t *testing.T, def agent.Definition, key, want string) {
	t.Helper()
	var settings struct {
		Graph json.RawMessage `json:"graph"`
	}
	if err := json.Unmarshal(def.Engine.Settings, &settings); err != nil {
		t.Fatalf("decode engine settings: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(settings.Graph))
	dec.UseNumber()
	var graph any
	if err := dec.Decode(&graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	got, ok := findNumber(graph, key)
	if !ok {
		t.Fatalf("graph number %q not found", key)
	}
	if got.String() != want {
		t.Errorf("graph number %q = %s, want %s", key, got.String(), want)
	}
}

func TestCreateRegistersAndPersists(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	spec := NewSpec("researcher", "Reads and summarizes the codebase", testGraph)
	result, err := lc.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Name != "researcher" {
		t.Fatalf("result name = %q, want researcher", result.Name)
	}
	defs := reg.definitions()
	def, ok := defs["researcher"]
	if !ok {
		t.Fatal("agent not registered")
	}
	if def.Card.Name != "researcher" || def.Card.Description != spec.Card.Description {
		t.Errorf("card = %+v", def.Card)
	}
	if def.Engine.Kind != "agent.Engine" || def.Engine.Impl != "graph" {
		t.Errorf("engine = %s/%s", def.Engine.Kind, def.Engine.Impl)
	}
	if len(def.Prepare) != 1 || def.Prepare[0].Type != "opencraft.prepare" {
		t.Errorf("prepare hooks = %+v, want opencraft.prepare", def.Prepare)
	}
	var engineSettings map[string]any
	if err := json.Unmarshal(def.Engine.Settings, &engineSettings); err != nil {
		t.Fatalf("decode engine settings: %v", err)
	}
	assertGraphMatches(t, engineSettings["graph"], testGraph)

	// Persisted declaration round-trips.
	data, err := os.ReadFile(filepath.Join(dir, "researcher", specFile))
	if err != nil {
		t.Fatalf("read persisted spec: %v", err)
	}
	var persisted AgentSpec
	if err := yaml.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode persisted spec: %v", err)
	}
	if persisted.Card.Name != "researcher" ||
		!reflect.DeepEqual(
			graphObject(t, mustGraphText(t, persisted)),
			graphObject(t, mustGraphText(t, spec))) {
		t.Errorf("persisted = %+v", persisted)
	}
}

// TestPersistedDeclarationUsesDefinitionShape verifies agent.yaml is
// written in the flowcraft Definition subset (card + engine.settings)
// instead of the legacy flat name/description/graph record.
func TestPersistedDeclarationUsesDefinitionShape(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	if _, err := lc.Create(context.Background(),
		NewSpec("researcher", "Reads and summarizes the codebase", testGraph),
	); err != nil {
		t.Fatalf("Create: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "researcher", specFile))
	if err != nil {
		t.Fatalf("read persisted spec: %v", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode persisted yaml: %v", err)
	}
	if _, legacy := raw["graph"]; legacy {
		t.Fatal("persisted declaration still uses the legacy top-level graph field")
	}
	if raw["version"] != float64(specVersion) {
		t.Errorf("persisted version = %v, want %d", raw["version"], specVersion)
	}
	if _, ok := raw["card"]; !ok {
		t.Fatal("persisted declaration missing card")
	}
	engine, ok := raw["engine"].(map[string]any)
	if !ok {
		t.Fatalf("persisted engine = %T, want object", raw["engine"])
	}
	settings, ok := engine["settings"].(map[string]any)
	if !ok {
		t.Fatalf("persisted engine settings = %T, want object", engine["settings"])
	}
	if _, ok := settings["graph"].(map[string]any); !ok {
		t.Fatalf("persisted settings.graph = %T, want inline object", settings["graph"])
	}
	assertGraphMatches(t, settings["graph"], testGraph)
	if _, ok := settings["build"]; ok {
		t.Error("persisted declaration carries host-owned build settings; they must stay injected")
	}
}

// TestLegacyDeclarationLoads verifies declarations written before the
// Definition-shaped format still load and register (the file is
// upgraded on the next update).
func TestLegacyDeclarationLoads(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	legacyDir := filepath.Join(dir, "legacy")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(map[string]any{
		"name":        "legacy",
		"description": "old shape",
		"graph":       testGraph,
	})
	if err != nil {
		t.Fatalf("marshal legacy declaration: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(legacyDir, specFile), data, 0o600,
	); err != nil {
		t.Fatal(err)
	}

	failures := lc.LoadAll(context.Background())
	if len(failures) != 0 {
		t.Fatalf("LoadAll failures = %+v, want none", failures)
	}
	def, ok := reg.definitions()["legacy"]
	if !ok {
		t.Fatal("legacy agent not registered")
	}
	if def.Card.Name != "legacy" || def.Card.Description != "old shape" {
		t.Errorf("legacy card = %+v", def.Card)
	}
	var engineSettings map[string]any
	if err := json.Unmarshal(def.Engine.Settings, &engineSettings); err != nil {
		t.Fatalf("decode engine settings: %v", err)
	}
	assertGraphMatches(t, engineSettings["graph"], testGraph)
}

// TestHandAuthoredInlineObjectLoads verifies the hand-friendly file
// shape: versioned, strict, with the graph written as an inline YAML
// object under engine.settings.graph.
func TestHandAuthoredInlineObjectLoads(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	handDir := filepath.Join(dir, "hand")
	if err := os.MkdirAll(handDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(map[string]any{
		"version": specVersion,
		"card": map[string]any{
			"name":        "hand",
			"description": "hand authored",
		},
		"engine": map[string]any{
			"kind": "agent.Engine",
			"impl": "graph",
			"settings": map[string]any{
				"graph": graphObject(t, testGraph),
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal hand-authored declaration: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(handDir, specFile), data, 0o600,
	); err != nil {
		t.Fatal(err)
	}

	failures := lc.LoadAll(context.Background())
	if len(failures) != 0 {
		t.Fatalf("LoadAll failures = %+v, want none", failures)
	}
	def, ok := reg.definitions()["hand"]
	if !ok {
		t.Fatal("hand-authored agent not registered")
	}
	if def.Card.Name != "hand" || def.Card.Description != "hand authored" {
		t.Errorf("hand card = %+v", def.Card)
	}
}

// TestGraphIntegerPrecisionSurvivesRegistration verifies numbers are
// not rounded through float64 on the way to the runtime: an integer
// beyond 2^53 must keep its exact digits in engine settings.graph
// whether it arrived as tool wire text or a hand-authored inline
// YAML object.
func TestGraphIntegerPrecisionSurvivesRegistration(t *testing.T) {
	const bigGraph = `{"name":"big","entry":"llm","nodes":[{"id":"llm","type":"inference","config":{"system_prompt":"SP","precision_guard":9007199254740993}}],"edges":[{"from":"llm","to":"__end__"}]}`

	t.Run("tool wire text", func(t *testing.T) {
		reg := newFakeRegistrar()
		lc, _ := newTestLifecycle(t, reg)
		if _, err := lc.Create(context.Background(),
			NewSpec("big", "precision guard", bigGraph),
		); err != nil {
			t.Fatalf("Create: %v", err)
		}
		def, ok := reg.definitions()["big"]
		if !ok {
			t.Fatal("agent not registered")
		}
		assertGraphNumber(t, def, "precision_guard", "9007199254740993")
	})

	t.Run("hand authored inline object", func(t *testing.T) {
		reg := newFakeRegistrar()
		lc, dir := newTestLifecycle(t, reg)
		agentDir := filepath.Join(dir, "big")
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		data := []byte(`version: 1
card:
  name: big
  description: precision guard
engine:
  kind: agent.Engine
  impl: graph
  settings:
    graph:
      name: big
      entry: llm
      nodes:
        - id: llm
          type: inference
          config:
            system_prompt: SP
            precision_guard: 9007199254740993
      edges:
        - from: llm
          to: __end__
`)
		if err := os.WriteFile(
			filepath.Join(agentDir, specFile), data, 0o600,
		); err != nil {
			t.Fatal(err)
		}
		failures := lc.LoadAll(context.Background())
		if len(failures) != 0 {
			t.Fatalf("LoadAll failures = %+v, want none", failures)
		}
		def, ok := reg.definitions()["big"]
		if !ok {
			t.Fatal("agent not registered")
		}
		assertGraphNumber(t, def, "precision_guard", "9007199254740993")
	})
}

// TestLoadAllRejectsHostOwnedKeys verifies strict decoding: a
// hand-authored file that looks like a full flowcraft Definition
// (prepare, policy, ...) is rejected instead of silently dropping the
// host-owned wiring.
func TestLoadAllRejectsHostOwnedKeys(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	evilDir := filepath.Join(dir, "evil")
	if err := os.MkdirAll(evilDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(map[string]any{
		"version": specVersion,
		"card": map[string]any{
			"name":        "evil",
			"description": "must be rejected",
		},
		"engine": map[string]any{
			"kind": "agent.Engine",
			"impl": "graph",
			"settings": map[string]any{
				"graph": graphObject(t, testGraph),
			},
		},
		"prepare": []any{map[string]any{"type": "opencraft.prepare"}},
	})
	if err != nil {
		t.Fatalf("marshal declaration: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(evilDir, specFile), data, 0o600,
	); err != nil {
		t.Fatal(err)
	}

	failures := lc.LoadAll(context.Background())
	if len(failures) != 1 {
		t.Fatalf("LoadAll failures = %+v, want 1 (host-owned prepare key)", failures)
	}
	if len(reg.definitions()) != 0 {
		t.Errorf("agent registered despite invalid declaration: %+v", reg.definitions())
	}
}

// TestLoadAllRejectsUnsupportedVersion guards future format drift:
// unknown versions fail loudly instead of being guessed at.
func TestLoadAllRejectsUnsupportedVersion(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	futureDir := filepath.Join(dir, "future")
	if err := os.MkdirAll(futureDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(map[string]any{
		"version": specVersion + 1,
		"card": map[string]any{
			"name":        "future",
			"description": "unknown version",
		},
		"engine": map[string]any{
			"kind": "agent.Engine",
			"impl": "graph",
			"settings": map[string]any{
				"graph": graphObject(t, testGraph),
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal declaration: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(futureDir, specFile), data, 0o600,
	); err != nil {
		t.Fatal(err)
	}

	failures := lc.LoadAll(context.Background())
	if len(failures) != 1 {
		t.Fatalf("LoadAll failures = %+v, want 1 (unsupported version)", failures)
	}
	if len(reg.definitions()) != 0 {
		t.Errorf("agent registered despite unsupported version: %+v", reg.definitions())
	}
}

func TestCreateRollsBackOnPersistFailure(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	// Make the agent directory unwritable so writeSpec fails after the
	// runtime registration succeeded.
	if err := os.MkdirAll(filepath.Join(dir, "doomed"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "doomed"), 0o700) })

	if _, err := lc.Create(context.Background(), NewSpec("doomed", "desc", testGraph)); err == nil {
		t.Fatal("Create should fail when persistence fails")
	}
	if len(reg.definitions()) != 0 {
		t.Errorf("registration not rolled back: %+v", reg.definitions())
	}
	if len(reg.removed) != 1 || reg.removed[0] != "doomed" {
		t.Errorf("rollback removals = %v, want [doomed]", reg.removed)
	}
}

func TestUpdateSwapsRegistrationAndPersists(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	orig := NewSpec("researcher", "old description", testGraph)
	created, err := lc.Create(context.Background(), orig)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newDesc := "new description"
	newGraph := `{"name":"g2","entry":"llm","nodes":[{"id":"llm","type":"inference","config":{"system_prompt":"SP2","tool_pending_key":"tool_pending"}}],"edges":[{"from":"llm","to":"__end__","condition":"tool_pending == false"}]}`
	result, err := lc.Update(context.Background(), "researcher", newDesc, newGraph)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.Name != "researcher" || result.Description != newDesc {
		t.Errorf("result = %+v", result)
	}

	// The runtime registration was swapped (drain first, then the new
	// definition) and the original created_at is preserved.
	if len(reg.removed) != 1 || reg.removed[0] != "researcher" {
		t.Errorf("removals = %v, want [researcher]", reg.removed)
	}
	def, ok := reg.definitions()["researcher"]
	if !ok {
		t.Fatal("updated agent not registered")
	}
	if def.Card.Description != newDesc {
		t.Errorf("card description = %q, want %q", def.Card.Description, newDesc)
	}
	var engineSettings map[string]any
	if err := json.Unmarshal(def.Engine.Settings, &engineSettings); err != nil {
		t.Fatalf("decode engine settings: %v", err)
	}
	assertGraphMatches(t, engineSettings["graph"], newGraph)

	// The persisted declaration round-trips the new spec.
	data, err := os.ReadFile(filepath.Join(dir, "researcher", specFile))
	if err != nil {
		t.Fatalf("read persisted spec: %v", err)
	}
	var persisted AgentSpec
	if err := yaml.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode persisted spec: %v", err)
	}
	if persisted.Card.Description != newDesc ||
		!reflect.DeepEqual(
			graphObject(t, mustGraphText(t, persisted)),
			graphObject(t, newGraph)) {
		t.Errorf("persisted = %+v", persisted)
	}
	if !persisted.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("created_at changed: %s -> %s", created.CreatedAt, persisted.CreatedAt)
	}
}

func TestUpdatePartialFieldKeepsRest(t *testing.T) {
	reg := newFakeRegistrar()
	lc, _ := newTestLifecycle(t, reg)
	if _, err := lc.Create(context.Background(), NewSpec("worker", "old description", testGraph)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := lc.Update(context.Background(), "worker", "new description", ""); err != nil {
		t.Fatalf("Update: %v", err)
	}
	def, ok := reg.definitions()["worker"]
	if !ok {
		t.Fatal("agent not registered")
	}
	if def.Card.Description != "new description" {
		t.Errorf("description = %q, want updated", def.Card.Description)
	}
	var engineSettings map[string]any
	if err := json.Unmarshal(def.Engine.Settings, &engineSettings); err != nil {
		t.Fatalf("decode engine settings: %v", err)
	}
	assertGraphMatches(t, engineSettings["graph"], testGraph)
}

func TestUpdateMissingAgentFails(t *testing.T) {
	lc, _ := newTestLifecycle(t, newFakeRegistrar())
	if _, err := lc.Update(context.Background(), "ghost", "desc", testGraph); err == nil {
		t.Fatal("Update of missing agent succeeded")
	} else if !errdefs.IsNotFound(err) {
		t.Errorf("error = %v, want NotFound", err)
	}
}

func TestUpdateNothingToUpdateFails(t *testing.T) {
	reg := newFakeRegistrar()
	lc, _ := newTestLifecycle(t, reg)
	if _, err := lc.Create(context.Background(), NewSpec("worker", "desc", testGraph)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := lc.Update(context.Background(), "worker", "", ""); err == nil {
		t.Fatal("empty update succeeded")
	} else if !errdefs.IsValidation(err) {
		t.Errorf("error = %v, want validation", err)
	}
	if len(reg.removed) != 0 {
		t.Errorf("runtime touched on no-op update: removals = %v", reg.removed)
	}
}

func TestUpdateNoChangeIsNoop(t *testing.T) {
	reg := newFakeRegistrar()
	lc, _ := newTestLifecycle(t, reg)
	spec := NewSpec("worker", "desc", testGraph)
	if _, err := lc.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := lc.Update(
		context.Background(), "worker", spec.Card.Description, mustGraphText(t, spec)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(reg.removed) != 0 {
		t.Errorf("no-change update should not drain: removals = %v", reg.removed)
	}
	if len(reg.definitions()) != 1 {
		t.Errorf("registrations = %+v, want untouched", reg.definitions())
	}
}

func TestUpdateRegisterFailureRestoresOld(t *testing.T) {
	reg := newFakeRegistrar()
	lc, _ := newTestLifecycle(t, reg)
	orig := NewSpec("worker", "old description", testGraph)
	if _, err := lc.Create(context.Background(), orig); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The next RegisterAgent (the swap) fails once; the restore
	// registration afterwards must succeed and carry the old spec.
	reg.registerFails = 1
	if _, err := lc.Update(context.Background(), "worker", "new description", testGraph); err == nil {
		t.Fatal("Update succeeded despite register failure")
	}
	def, ok := reg.definitions()["worker"]
	if !ok {
		t.Fatal("old registration not restored")
	}
	if def.Card.Description != orig.Card.Description {
		t.Errorf("restored description = %q, want %q",
			def.Card.Description, orig.Card.Description)
	}
}

func TestUpdateRollsBackOnPersistFailure(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	orig := NewSpec("doomed", "old description", testGraph)
	if _, err := lc.Create(context.Background(), orig); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Make the agent directory unwritable so writeSpec fails after the
	// runtime swap already happened.
	if err := os.Chmod(filepath.Join(dir, "doomed"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "doomed"), 0o700) })

	if _, err := lc.Update(context.Background(), "doomed", "new description", testGraph); err == nil {
		t.Fatal("Update should fail when persistence fails")
	}
	def, ok := reg.definitions()["doomed"]
	if !ok {
		t.Fatal("old registration not restored after persist failure")
	}
	if def.Card.Description != orig.Card.Description {
		t.Errorf("restored description = %q, want %q",
			def.Card.Description, orig.Card.Description)
	}
	// The update drained once for the swap and the restore drained the
	// half-swapped definition before re-registering the old one.
	if len(reg.removed) != 2 || reg.removed[0] != "doomed" || reg.removed[1] != "doomed" {
		t.Errorf("removals = %v, want [doomed doomed]", reg.removed)
	}
}

func TestUpdateUnregisterFailureAborts(t *testing.T) {
	reg := newFakeRegistrar()
	lc, _ := newTestLifecycle(t, reg)
	orig := NewSpec("worker", "old description", testGraph)
	if _, err := lc.Create(context.Background(), orig); err != nil {
		t.Fatalf("Create: %v", err)
	}
	reg.unregisterErr = errdefs.Internalf("drain failed")
	if _, err := lc.Update(context.Background(), "worker", "new description", testGraph); err == nil {
		t.Fatal("Update succeeded despite drain failure")
	}
	def, ok := reg.definitions()["worker"]
	if !ok {
		t.Fatal("agent missing after aborted update")
	}
	if def.Card.Description != orig.Card.Description {
		t.Errorf("description = %q, want original %q",
			def.Card.Description, orig.Card.Description)
	}
}

func TestRemoveUnregistersAndDeletes(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	if _, err := lc.Create(context.Background(), NewSpec("worker", "desc", testGraph)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := lc.Remove(context.Background(), "worker"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(reg.definitions()) != 0 {
		t.Errorf("agent still registered: %+v", reg.definitions())
	}
	if _, err := os.Stat(filepath.Join(dir, "worker")); !os.IsNotExist(err) {
		t.Errorf("declaration dir not removed (err=%v)", err)
	}
}

func TestLoadAllRegistersPersisted(t *testing.T) {
	createReg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, createReg)
	if _, err := lc.Create(context.Background(), NewSpec("alpha", "first", testGraph)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Load into a fresh registrar: the runtime does not know about the
	// agent until startup re-registers it from the declaration.
	reg := newFakeRegistrar()
	lc.Bind(reg)
	// A broken declaration must not block loading the valid one.
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "broken", specFile),
		[]byte("name: [unclosed"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	lc.Bind(reg)
	failures := lc.LoadAll(context.Background())
	if len(failures) != 1 {
		t.Fatalf("LoadAll failures = %+v, want 1 (broken)", failures)
	}
	defs := reg.definitions()
	if len(defs) != 1 {
		t.Fatalf("registered = %+v, want [alpha]", defs)
	}
	if _, ok := defs["alpha"]; !ok {
		t.Errorf("alpha not loaded: %+v", defs)
	}
}

// TestLoadMissingSkipsReboundAgentsAndRegistersNew mirrors an in-place
// reload: the new generation's registrar already carries the dynamic
// agents flowcraft re-bound, so LoadMissing must skip them while still
// registering declarations that appeared on disk since (or failed
// earlier).
func TestLoadMissingSkipsReboundAgentsAndRegistersNew(t *testing.T) {
	reg1 := newFakeRegistrar()
	lc1, dir := newTestLifecycle(t, reg1)
	if _, err := lc1.Create(context.Background(),
		NewSpec("alpha", "first", testGraph),
	); err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	alphaDef := reg1.definitions()["alpha"]

	reg2 := newFakeRegistrar()
	if _, err := reg2.RegisterAgent(
		context.Background(), "alpha", alphaDef,
		runtimecore.WithToolAssembly(toolAssemblyResource),
	); err != nil {
		t.Fatalf("preload rebound alpha: %v", err)
	}
	lc2, err := New(dir, "", "")
	if err != nil {
		t.Fatalf("New lifecycle: %v", err)
	}
	lc2.Bind(reg2)
	lc2.AdoptKnown(lc1)
	// beta exists on disk but was never registered in this generation:
	// LoadMissing must pick it up without replaying alpha.
	if err := lc2.writeSpec(NewSpec("beta", "second", testGraph)); err != nil {
		t.Fatalf("write beta declaration: %v", err)
	}

	failures := lc2.LoadMissing(context.Background())
	if len(failures) != 0 {
		t.Fatalf("LoadMissing failures = %+v, want none", failures)
	}
	defs := reg2.definitions()
	if _, ok := defs["alpha"]; !ok {
		t.Error("alpha missing after LoadMissing")
	}
	if _, ok := defs["beta"]; !ok {
		t.Error("beta not registered by LoadMissing")
	}
	if len(defs) != 2 {
		t.Errorf("registered = %+v, want [alpha beta]", defs)
	}
}

// TestLoadMissingTreatsConflictAsLive mirrors the reload race that the
// known-set handoff cannot fully close: an agent becomes live in the
// runtime after AdoptKnown ran (a Create racing the reload window, or
// flowcraft re-binding an entry the lifecycle never learned), so the
// new lifecycle still has the declaration on disk and unknown.
// LoadMissing must treat the registration conflict as "already live"
// and remember the name instead of reporting a failure that would
// otherwise repeat on every reload until restart.
func TestLoadMissingTreatsConflictAsLive(t *testing.T) {
	seedReg := newFakeRegistrar()
	seedLC, dir := newTestLifecycle(t, seedReg)
	if _, err := seedLC.Create(context.Background(),
		NewSpec("alpha", "first", testGraph),
	); err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	alphaDef := seedReg.definitions()["alpha"]

	// alpha is already live in the runtime, but the lifecycle that is
	// about to load never adopted the name.
	reg := newFakeRegistrar()
	if _, err := reg.RegisterAgent(
		context.Background(), "alpha", alphaDef,
		runtimecore.WithToolAssembly(toolAssemblyResource),
	); err != nil {
		t.Fatalf("preload live alpha: %v", err)
	}
	lc, err := New(dir, "", "")
	if err != nil {
		t.Fatalf("New lifecycle: %v", err)
	}
	lc.Bind(reg)

	failures := lc.LoadMissing(context.Background())
	if len(failures) != 0 {
		t.Fatalf("LoadMissing failures = %+v, want none (alpha is already live)", failures)
	}
	if got := reg.definitions(); len(got) != 1 {
		t.Errorf("registered = %+v, want [alpha]", got)
	}
	if len(reg.removed) != 0 {
		t.Errorf("conflict handling unregistered the live agent: removals = %v", reg.removed)
	}
	// The conflict marked alpha known, so a second pass (and later
	// reloads that adopt this lifecycle) must not retry it.
	if failures := lc.LoadMissing(context.Background()); len(failures) != 0 {
		t.Fatalf("second LoadMissing failures = %+v, want none", failures)
	}
}

func TestListSorted(t *testing.T) {
	lc, _ := newTestLifecycle(t, newFakeRegistrar())
	for _, name := range []string{"zulu", "alpha"} {
		if _, err := lc.Create(context.Background(), NewSpec(name, "desc "+name, testGraph)); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	list := lc.List()
	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "zulu" {
		t.Fatalf("List = %+v, want [alpha zulu]", list)
	}
}

func TestSpecValidate(t *testing.T) {
	base := NewSpec("ok-agent-1", "desc", testGraph)
	if err := base.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	for _, bad := range []AgentSpec{
		NewSpec("Bad", "d", testGraph),
		NewSpec("bad_name", "d", testGraph),
		NewSpec("ok", "", testGraph),
		NewSpec("ok", "d", ""),
		NewSpec("ok", "d", "{broken"),
		func() AgentSpec {
			spec := NewSpec("ok", "d", testGraph)
			spec.Engine.Deps = resource.Deps{"memory": "mem"}
			return spec
		}(),
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("spec %+v accepted, want error", bad)
		}
	}
}

// TestListEmptyReturnsNonNil verifies the agents list is never null:
// the desktop UI iterates the result directly (agents.length).
func TestListEmptyReturnsNonNil(t *testing.T) {
	reg := newFakeRegistrar()
	lc, _ := newTestLifecycle(t, reg)
	got := lc.List()
	if got == nil {
		t.Fatal("List returned nil; want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("List = %d entries, want 0", len(got))
	}
}
