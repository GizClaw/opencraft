package agents

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	"sigs.k8s.io/yaml"
)

// fakeRegistrar records registration/removal calls so tests can assert
// the lifecycle drives the runtime correctly.
type fakeRegistrar struct {
	mu            sync.Mutex
	registered    map[string]agent.Definition
	removed       []string
	registerErr   error
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
		Name:         "researcher",
		Description:  "Reads and summarizes the codebase",
		Instructions: "Explore the repo and summarize the architecture.",
		Tools:        ToolsReadOnly,
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

	// Persisted declaration round-trips.
	data, err := os.ReadFile(filepath.Join(dir, "researcher", specFile))
	if err != nil {
		t.Fatalf("read persisted spec: %v", err)
	}
	var persisted AgentSpec
	if err := yaml.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode persisted spec: %v", err)
	}
	if persisted.Name != "researcher" || persisted.Instructions != spec.Instructions ||
		persisted.Tools != ToolsReadOnly {
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
		Name:         "doomed",
		Description:  "desc",
		Instructions: "do it",
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

func TestRemoveUnregistersAndDeletes(t *testing.T) {
	reg := newFakeRegistrar()
	lc, dir := newTestLifecycle(t, reg)
	if _, err := lc.Create(context.Background(), AgentSpec{
		Name:         "worker",
		Description:  "desc",
		Instructions: "do it",
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
		Name:         "alpha",
		Description:  "first",
		Instructions: "task alpha",
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
			Name:         name,
			Description:  "desc " + name,
			Instructions: "task",
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
		Name:         "ok-agent-1",
		Description:  "desc",
		Instructions: "do it",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	for _, bad := range []AgentSpec{
		{Name: "Bad", Description: "d", Instructions: "i"},
		{Name: "bad_name", Description: "d", Instructions: "i"},
		{Name: "ok", Description: "", Instructions: "i"},
		{Name: "ok", Description: "d", Instructions: ""},
		{Name: "ok", Description: "d", Instructions: "i", Model: "noprofile"},
		{Name: "ok", Description: "d", Instructions: "i", ThinkLevel: "ultra"},
		{Name: "ok", Description: "d", Instructions: "i", Tools: "write"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("spec %+v accepted, want error", bad)
		}
	}
}
