package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
)

// TestBuiltInCatalogBuildsRuntime keeps the shipped inference catalog
// deployable: every template and every model entry is written as a real
// user configuration and handed to the released drivers. A capability,
// effort map, or kind the driver rejects fails here instead of on the
// settings page, which is the only reason a built-in entry can be
// trusted without a vendor table in the runtime.
func TestBuiltInCatalogBuildsRuntime(t *testing.T) {
	catalog, err := config.LoadInferenceCatalog()
	if err != nil {
		t.Fatalf("load inference catalog: %v", err)
	}
	for _, template := range catalog.Templates() {
		t.Run("template/"+template.ID, func(t *testing.T) {
			models, err := catalog.TemplateModels(template)
			if err != nil {
				t.Fatal(err)
			}
			declared := make([]config.Model, 0, len(models))
			for _, m := range models {
				declared = append(declared, m.Lower())
			}
			buildCatalogRuntime(t, config.Instance{
				StableID:  "inst-catalog",
				Type:      template.Type,
				Name:      template.Label,
				API:       template.API,
				Endpoint:  template.Endpoint,
				Advanced:  template.Advanced,
				KeySource: config.KeyEnv,
				Enabled:   true,
				Models:    declared,
			})
		})
	}
	for _, entry := range catalog.Models() {
		t.Run("model/"+entry.ID, func(t *testing.T) {
			instance := config.Instance{
				StableID:  "inst-catalog",
				Type:      entry.Type,
				Name:      entry.Label,
				KeySource: config.KeyEnv,
				Enabled:   true,
				Models:    []config.Model{entry.Lower()},
			}
			// A model that accepts video input needs the endpoint fact
			// that carries it: only the chat surface lowers video, and
			// only when the deployment states wire.video_input. The
			// template pins it; a bare instance must state it too, or
			// the driver rejects the declaration.
			if declaresVideoInput(entry.ModelSpec) {
				instance.API = "chat"
				instance.Advanced.VideoInput = true
			}
			buildCatalogRuntime(t, instance)
		})
	}
}

// declaresVideoInput reports whether one catalog model accepts video
// content, the declaration that depends on an endpoint fact.
func declaresVideoInput(spec config.ModelSpec) bool {
	for _, input := range spec.Capabilities.Inputs {
		if input == message.PartVideo {
			return true
		}
	}
	return false
}

// buildCatalogRuntime writes one catalog instance as the user layer and
// builds the runtime from it.
func buildCatalogRuntime(t *testing.T, instance config.Instance) {
	t.Helper()
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, prov := range config.Providers {
		if prov.EnvVar != "" {
			t.Setenv(prov.EnvVar, "catalog-test-key")
		}
	}
	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	if err := configseed.Write(userDir, config.InferenceConfig{
		Instances: []config.Instance{instance},
	}); err != nil {
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
	sessionStore := migratedSessionStore(t, layout)
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
			return sessionStore, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	defer func() { _ = rt.Close() }()
}
