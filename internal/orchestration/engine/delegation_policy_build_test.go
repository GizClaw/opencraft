package engine

import (
	"context"
	"path/filepath"
	"testing"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
)

// TestEmptySettingsSavesKeepWorkspaceBuildable pins the save -> assemble
// path behind every settings card, each one in the state that broke it:
// a settings struct whose fields are all omitempty used to be written
// as `settings: {}`.
//
// flowcraft reads a resource's settings subtree as a whole-subtree
// source and rejects an empty object, so saving the delegation card
// with both target lists empty failed the build of the entire document
// ("runtime build deployment: deploy: resource \"delegate.policy\":
// ... empty object is not valid") and left every workspace
// unassemblable until the layer was repaired by hand. The other cards
// escaped that only because their resources carry embedded settings the
// empty object merged into.
func TestEmptySettingsSavesKeepWorkspaceBuildable(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	if err := configseed.Write(userDir, config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models:    []config.Model{{Name: "test-model"}},
		}},
	}); err != nil {
		t.Fatalf("write inference config: %v", err)
	}
	// Every settings card in the state that writes no values at all:
	// delegation with both target lists cleared (the card always submits
	// the lists, so this is "no restriction"), the rest with the zero
	// value their settings page sends before anything is configured.
	if err := config.SaveDelegation(userDir, config.DelegationSettings{
		MaxConcurrency: 8,
		MaxDepth:       4,
	}); err != nil {
		t.Fatalf("save delegation settings: %v", err)
	}
	for name, save := range map[string]func() error{
		"review": func() error {
			return config.SaveReview(userDir, config.ReviewSettings{})
		},
		"user memory": func() error {
			return config.SaveUserMemory(userDir, config.UserMemorySettings{})
		},
		"skill lifecycle": func() error {
			return config.SaveSkillLifecycle(
				userDir, config.SkillLifecycleSettings{})
		},
		"web search": func() error {
			return config.SaveWebSearch(userDir, config.WebSearchSettings{})
		},
	} {
		if err := save(); err != nil {
			t.Fatalf("save %s settings: %v", name, err)
		}
	}

	doc, err := LoadDocument(context.Background(), userDir)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	for _, name := range []string{
		"delegate.policy", "review", "usermemory", "skilllifecycle",
		"tool.websearch",
	} {
		settings := string(doc.Resources[name].Settings)
		if settings == "" || settings == "{}" || settings == "null" {
			t.Errorf("merged %s settings = %q, want a readable source",
				name, settings)
		}
	}

	layout := testWorkspaceLayout(t, home, work)
	sessionStore := migratedSessionStore(t, layout)
	rt, err := BuildRuntime(
		context.Background(),
		doc,
		WithAutomationHost(automationStub{}),
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithWorkspaceLayout(layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return sessionStore, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	defer func() { _ = rt.Close() }()

	// A cleared policy is not a locked-down one: the built resource
	// allows every registered target.
	value, ok := rt.Resource("delegate.policy")
	if !ok {
		t.Fatal("delegate.policy resource missing")
	}
	policy, ok := value.(*subagents.Policy)
	if !ok {
		t.Fatalf("delegate.policy resource is %T, want *subagents.Policy", value)
	}
	if !policy.Empty() {
		t.Fatalf("cleared policy restricts: %s", policy.Describe())
	}
}
