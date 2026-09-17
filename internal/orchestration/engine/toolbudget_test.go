package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// definitionBytes mirrors the core's per-definition budget accounting so
// the assertions below talk about the same numbers the injection policy
// does.
func definitionBytes(def message.ToolDefinition) int64 {
	return int64(len(def.Name) + len(def.Description) + len(def.InputSchema) + 32)
}

// TestAssistantToolBudgetKeepsDiscoveredToolsVisible pins the dynamic
// injection budget of the deployed assistant. tool_search reports a tool
// as exposed once it enters the discovery pool, and the model relies on
// that promise in the next round: if the per-round visible budget then
// truncates the pool away, the model's call is rejected as "not exposed
// in this round's tool set" and it burns rounds re-searching instead of
// working (observed with generate_image/generate_video, whose schemas
// are the largest in the catalog).
//
// The always-visible baseline already measures ~11 KiB, so the core
// default of a 16 KiB visible budget / 16 KiB pool leaves no room for a
// realistic tool_search batch. This test discovers one such batch and
// requires every pool entry to reach the round's definitions.
func TestAssistantToolBudgetKeepsDiscoveredToolsVisible(t *testing.T) {
	asm := buildEmbeddedToolAssembly(t)
	session := asm.NewSession()

	baseline := visibleBytes(session)
	if baseline <= 0 {
		t.Fatal("always-visible baseline is empty")
	}

	// One tool_search batch: the media tools that triggered the loop plus
	// a plausible mix of agent, delegation, skill and web tools.
	batch := []string{
		"generate_video",
		"generate_image",
		"create_agent",
		"delegate",
		"update_agent",
		"web_fetch",
		"skill_create",
		"skill_modify",
	}
	outcome := session.Discover(batch...)
	for _, result := range outcome.Results {
		if !result.Exposed {
			t.Fatalf("tool %q did not enter the discovery pool: %s",
				result.Name, result.Reason)
		}
	}
	// The pool promise lands "starting from the next round", so the round
	// that follows the search advances before building its definitions.
	session.AdvanceTurn()

	visible := make(map[string]bool)
	for _, def := range session.Definitions() {
		visible[def.Name] = true
	}
	var batchBytes int64
	for _, def := range asm.Catalog().Definitions() {
		for _, name := range batch {
			if def.Name == name {
				batchBytes += definitionBytes(def)
			}
		}
	}
	for _, name := range batch {
		if !visible[name] {
			t.Errorf("tool %q is in the discovery pool but missing from the "+
				"round's visible set", name)
		}
	}
	t.Logf("baseline %d bytes, discovered batch %d bytes", baseline, batchBytes)
	if baseline+batchBytes <= 16*1024 {
		t.Errorf("batch (%d bytes) plus baseline (%d bytes) fits the core "+
			"default budget; the regression this test pins would not "+
			"reproduce", batchBytes, baseline)
	}
}

// visibleBytes sums the round's visible definitions.
func visibleBytes(session tool.Session) int64 {
	var total int64
	for _, def := range session.Definitions() {
		total += definitionBytes(def)
	}
	return total
}

// buildEmbeddedToolAssembly assembles the embedded deploy and returns
// the assistant's tool assembly.
func buildEmbeddedToolAssembly(t *testing.T) *tool.Assembly {
	t.Helper()
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models:    []config.Model{{Name: "test-model"}},
		}},
	}
	if err := config.WriteInference(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}
	mgr, err := config.Open(config.Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	layout := testWorkspaceLayout(t, home, work)
	store := migratedSessionStore(t, layout)
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithAutomationHost(automationStub{}),
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithWorkspaceLayout(layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return store, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	value, ok := rt.Resource("tools")
	if !ok {
		t.Fatal("tools resource missing")
	}
	asm, ok := value.(*tool.Assembly)
	if !ok {
		t.Fatalf("tools resource is %T", value)
	}
	return asm
}
