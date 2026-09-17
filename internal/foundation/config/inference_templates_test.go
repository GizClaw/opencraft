package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
)

// TestInferenceCatalogIsUsable keeps the shipped catalog on the
// settings-page write path: every template must lower, normalize, and
// render as a real save, because the settings page prefills rows from it
// without any further validation.
func TestInferenceCatalogIsUsable(t *testing.T) {
	setCatalogEnv(t)
	catalog, err := LoadInferenceCatalog()
	if err != nil {
		t.Fatalf("load inference catalog: %v", err)
	}
	if catalog.Version() == "" {
		t.Fatal("inference catalog has no version")
	}
	templates := catalog.Templates()
	if len(templates) == 0 {
		t.Fatal("inference catalog declares no templates")
	}
	for _, template := range templates {
		models, err := catalog.TemplateModels(template)
		if err != nil {
			t.Fatalf("template %s: resolve models: %v", template.ID, err)
		}
		spec := InstanceSpec{
			Type:      template.Type,
			Name:      template.Label,
			API:       template.API,
			Endpoint:  template.Endpoint,
			KeySource: KeySourceEnvName,
			Models:    models,
		}
		if _, err := spec.Lower(SourceUser, ""); err != nil {
			t.Fatalf("template %s: lower: %v", template.ID, err)
		}
	}
}

// TestInferenceTemplateRoundTripsThroughSave saves every shipped
// template the way the settings page does and reads it back, so a
// template that only lowers but cannot be written (or read back) fails
// here rather than in the page.
func TestInferenceTemplateRoundTripsThroughSave(t *testing.T) {
	setCatalogEnv(t)
	catalog, err := LoadInferenceCatalog()
	if err != nil {
		t.Fatalf("load inference catalog: %v", err)
	}
	for _, template := range catalog.Templates() {
		t.Run(template.ID, func(t *testing.T) {
			models, err := catalog.TemplateModels(template)
			if err != nil {
				t.Fatalf("resolve models: %v", err)
			}
			dir := t.TempDir()
			_, err = ApplySettingsSave(dir, SaveRequest{
				Instances: []InstanceSpec{{
					Type:      template.Type,
					Name:      template.Label,
					API:       template.API,
					Endpoint:  template.Endpoint,
					Advanced:  template.Advanced,
					KeySource: KeySourceEnvName,
					Models:    models,
				}},
				Router: DefaultRouterPolicy(),
			}, nil)
			if err != nil {
				t.Fatalf("save template: %v", err)
			}
			cfg, err := LoadInference(dir)
			if err != nil {
				t.Fatalf("load saved template: %v", err)
			}
			if len(cfg.Instances) != 1 {
				t.Fatalf("saved %d instances, want 1", len(cfg.Instances))
			}
			got := cfg.Instances[0]
			if got.Type != template.Type {
				t.Fatalf("saved type %q, want %q", got.Type, template.Type)
			}
			if !reflect.DeepEqual(got.Advanced, template.Advanced) {
				t.Fatalf(
					"advanced round trip = %+v, want %+v",
					got.Advanced, template.Advanced,
				)
			}
			if len(got.Models) != len(models) {
				t.Fatalf("saved %d models, want %d", len(got.Models), len(models))
			}
			for i, want := range models {
				if spec := ModelToSpec(got.Models[i]); !reflect.DeepEqual(spec, want) {
					t.Fatalf(
						"model %d round trip = %+v, want %+v",
						i, spec, want,
					)
				}
			}
		})
	}
}

// TestParseInferenceCatalogRejectsBadDocuments pins the strict decode:
// a catalog typo must fail loudly instead of dropping a fact or naming a
// provider the deployment cannot build.
func TestParseInferenceCatalogRejectsBadDocuments(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{{
		name: "unknown field",
		doc: `{"version":"v1","models":[{"id":"openai/x","type":"openai",
			"name":"x","contxet_window":1}],"templates":[]}`,
		want: "unknown field",
	}, {
		name: "unknown provider type",
		doc: `{"version":"v1","models":[{"id":"kimi/x","type":"kimi",
			"name":"x"}],"templates":[]}`,
		want: "unknown provider type",
	}, {
		name: "dangling template model",
		doc: `{"version":"v1","models":[{"id":"openai/x","type":"openai",
			"name":"x"}],"templates":[{"id":"t","label":"T","type":"openai",
			"models":["openai/missing"]}]}`,
		want: "unknown model",
	}, {
		name: "invalid template endpoint",
		doc: `{"version":"v1","models":[{"id":"openai/x","type":"openai",
			"name":"x"}],"templates":[{"id":"t","label":"T","type":"openai",
			"endpoint":"not-a-url","models":["openai/x"]}]}`,
		want: "invalid endpoint",
	}, {
		name: "missing version",
		doc: `{"models":[{"id":"openai/x","type":"openai","name":"x"}],
			"templates":[]}`,
		want: "needs a version",
	}, {
		name: "unknown lifecycle status",
		doc: `{"version":"v1","models":[{"id":"openai/x","type":"openai",
			"name":"x","lifecycle":{"status":"gone"}}],"templates":[]}`,
		want: "lifecycle status",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseInferenceCatalog([]byte(tc.doc))
			if err == nil {
				t.Fatalf("parse accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestInferenceCatalogVideoTemplateIsComplete pins the one built-in
// template whose model declaration depends on an endpoint fact: the
// driver lowers video input only on a chat deployment that states
// wire.video_input, so the declaration and the fact ship together and
// neither half may be dropped on its own.
func TestInferenceCatalogVideoTemplateIsComplete(t *testing.T) {
	catalog, err := LoadInferenceCatalog()
	if err != nil {
		t.Fatalf("load inference catalog: %v", err)
	}
	var template InferenceCatalogTemplate
	found := false
	for _, candidate := range catalog.Templates() {
		if candidate.ID == "zhipu-glm-video" {
			template, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatal("catalog carries no zhipu-glm-video template")
	}
	if !template.Advanced.VideoInput {
		t.Fatal("zhipu-glm-video does not state wire.video_input")
	}
	if template.API != "chat" {
		t.Fatalf(
			"zhipu-glm-video uses api %q; video input lowers on chat only",
			template.API,
		)
	}
	models, err := catalog.TemplateModels(template)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("template carries %d models, want 1", len(models))
	}
	declared := false
	for _, input := range models[0].Capabilities.Inputs {
		if input == message.PartVideo {
			declared = true
		}
	}
	if !declared {
		t.Fatalf(
			"zhipu-glm-video model %s does not declare video input",
			models[0].Name,
		)
	}
}

// setCatalogEnv satisfies the env key source every template is checked
// with, so the tests exercise validation rather than credential
// availability.
func setCatalogEnv(t *testing.T) {
	t.Helper()
	for _, prov := range Providers {
		if prov.EnvVar != "" {
			t.Setenv(prov.EnvVar, "catalog-test-key")
		}
	}
}

// TestInferenceCatalogHostedSearchFacts pins the provider-side search
// declarations the catalog ships. DeepSeek's Responses API executes
// web_search on V4 Pro and ignores it for the flash models, so a row
// added from the catalog must start with the checkbox in the state the
// upstream can honour: a pre-ticked box that does nothing is worse than
// no box at all. These facts are maintenance data — when a provider
// changes what it honours, this test is where the catalog update lands.
func TestInferenceCatalogHostedSearchFacts(t *testing.T) {
	setCatalogEnv(t)
	catalog, err := LoadInferenceCatalog()
	if err != nil {
		t.Fatalf("load inference catalog: %v", err)
	}
	for id, want := range map[string]bool{
		"deepseek/deepseek-v4-pro":      true,
		"deepseek/deepseek-flash":       false,
		"openai/gpt-5.6-sol":            true,
		"bytedance/doubao-seed-2-1-pro": true,
	} {
		entry, ok := catalog.Model(id)
		if !ok {
			t.Fatalf("catalog model %s is missing", id)
		}
		if got := entry.Capabilities.HostedWebSearch; got != want {
			t.Errorf("catalog model %s hosted_web_search = %v, want %v",
				id, got, want)
		}
	}
}
