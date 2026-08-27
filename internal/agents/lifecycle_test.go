package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
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
	lc, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if reg != nil {
		lc.Bind(reg)
	}
	return lc, dir
}

func TestCreateRegistersAndPersists(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	spec := AgentSpec{
		Name:        "researcher",
		Description: "Reads and summarizes the codebase",
		Graph:       testGraph,
	}
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
	if def.Card.Name != "researcher" || def.Card.Description != spec.Description {
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
	if engineSettings["graph"] != testGraph {
		t.Errorf("engine settings graph = %v, want caller-supplied graph verbatim", engineSettings["graph"])
	}

	// Persisted declaration round-trips.
	data, err := os.ReadFile(filepath.Join(dir, "researcher", specFile))
	if err != nil {
		t.Fatalf("read persisted spec: %v", err)
	}
	var persisted AgentSpec
	if err := yaml.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode persisted spec: %v", err)
	}
	if persisted.Name != "researcher" || persisted.Graph != spec.Graph {
		t.Errorf("persisted = %+v", persisted)
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

	if _, err := lc.Create(context.Background(), AgentSpec{
		Name:        "doomed",
		Description: "desc",
		Graph:       testGraph,
	}); err == nil {
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
	orig := AgentSpec{
		Name:        "researcher",
		Description: "old description",
		Graph:       testGraph,
	}
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
	if engineSettings["graph"] != newGraph {
		t.Errorf("engine settings graph = %v, want new graph verbatim", engineSettings["graph"])
	}

	// The persisted declaration round-trips the new spec.
	data, err := os.ReadFile(filepath.Join(dir, "researcher", specFile))
	if err != nil {
		t.Fatalf("read persisted spec: %v", err)
	}
	var persisted AgentSpec
	if err := yaml.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode persisted spec: %v", err)
	}
	if persisted.Description != newDesc || persisted.Graph != newGraph {
		t.Errorf("persisted = %+v", persisted)
	}
	if !persisted.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("created_at changed: %s -> %s", created.CreatedAt, persisted.CreatedAt)
	}
}

func TestUpdatePartialFieldKeepsRest(t *testing.T) {
	reg := newFakeRegistrar()
	lc, _ := newTestLifecycle(t, reg)
	if _, err := lc.Create(context.Background(), AgentSpec{
		Name:        "worker",
		Description: "old description",
		Graph:       testGraph,
	}); err != nil {
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
	if engineSettings["graph"] != testGraph {
		t.Errorf("graph changed unexpectedly: %v", engineSettings["graph"])
	}
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
	if _, err := lc.Create(context.Background(), AgentSpec{
		Name:        "worker",
		Description: "desc",
		Graph:       testGraph,
	}); err != nil {
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
	spec := AgentSpec{
		Name:        "worker",
		Description: "desc",
		Graph:       testGraph,
	}
	if _, err := lc.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := lc.Update(context.Background(), "worker", spec.Description, spec.Graph); err != nil {
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
	orig := AgentSpec{
		Name:        "worker",
		Description: "old description",
		Graph:       testGraph,
	}
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
	if def.Card.Description != orig.Description {
		t.Errorf("restored description = %q, want %q", def.Card.Description, orig.Description)
	}
}

func TestUpdateRollsBackOnPersistFailure(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	orig := AgentSpec{
		Name:        "doomed",
		Description: "old description",
		Graph:       testGraph,
	}
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
	if def.Card.Description != orig.Description {
		t.Errorf("restored description = %q, want %q", def.Card.Description, orig.Description)
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
	orig := AgentSpec{
		Name:        "worker",
		Description: "old description",
		Graph:       testGraph,
	}
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
	if def.Card.Description != orig.Description {
		t.Errorf("description = %q, want original %q", def.Card.Description, orig.Description)
	}
}

func TestRemoveUnregistersAndDeletes(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	if _, err := lc.Create(context.Background(), AgentSpec{
		Name:        "worker",
		Description: "desc",
		Graph:       testGraph,
	}); err != nil {
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
	if _, err := lc.Create(context.Background(), AgentSpec{
		Name:        "alpha",
		Description: "first",
		Graph:       testGraph,
	}); err != nil {
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

func TestListSorted(t *testing.T) {
	lc, _ := newTestLifecycle(t, newFakeRegistrar())
	for _, name := range []string{"zulu", "alpha"} {
		if _, err := lc.Create(context.Background(), AgentSpec{
			Name:        name,
			Description: "desc " + name,
			Graph:       testGraph,
		}); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	list := lc.List()
	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "zulu" {
		t.Fatalf("List = %+v, want [alpha zulu]", list)
	}
}

func TestSpecValidate(t *testing.T) {
	base := AgentSpec{
		Name:        "ok-agent-1",
		Description: "desc",
		Graph:       testGraph,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	for _, bad := range []AgentSpec{
		{Name: "Bad", Description: "d", Graph: testGraph},
		{Name: "bad_name", Description: "d", Graph: testGraph},
		{Name: "ok", Description: "", Graph: testGraph},
		{Name: "ok", Description: "d", Graph: ""},
		{Name: "ok", Description: "d", Graph: "{broken"},
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
