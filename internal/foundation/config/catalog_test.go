package config

import "testing"

func TestModelCatalog(t *testing.T) {
	cat, err := ModelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]ProviderModels, len(cat))
	for _, p := range cat {
		byID[p.Provider] = p
	}
	if len(byID) != len(Providers) {
		t.Fatalf("catalog providers = %d, want %d", len(byID), len(Providers))
	}
	for provider, entry := range byID {
		for _, m := range entry.Models {
			if m.Inputs == nil || m.Outputs == nil {
				t.Fatalf(
					"provider %s model %s must serialize empty capability lists as [] not null",
					provider, m.Name)
			}
		}
	}
	if len(byID["azure"].Models) != 0 {
		t.Fatalf("azure must have no built-in catalog: %+v", byID["azure"].Models)
	}

	openai := make(map[string]ModelTemplate, len(byID["openai"].Models))
	for _, m := range byID["openai"].Models {
		openai[m.Name] = m
	}
	sol, ok := openai["gpt-5.6-sol"]
	if !ok {
		t.Fatal("openai catalog missing gpt-5.6-sol")
	}
	if sol.Reasoning != "toggle" || len(sol.ReasoningEffortMap) != 5 {
		t.Fatalf("gpt-5.6-sol template = %+v", sol)
	}
	if !sol.EffortNone {
		t.Fatal("gpt-5.6-sol must carry effort_none from the driver catalog")
	}
	if !openai["text-embedding-3-small"].Dimensions {
		t.Fatal("text-embedding-3-small must carry dimensions")
	}
	if repl := openai["gpt-5.4-nano"].Replacement; repl != "gpt-5.6-luna" {
		t.Fatalf("deprecated replacement = %q, want gpt-5.6-luna", repl)
	}
	if !openai["gpt-5.4-nano"].Deprecated {
		t.Fatal("gpt-5.4-nano must be marked deprecated")
	}

	deepseek := make(map[string]ModelTemplate, len(byID["deepseek"].Models))
	for _, m := range byID["deepseek"].Models {
		deepseek[m.Name] = m
	}
	flash := deepseek["deepseek-v4-flash"]
	if flash.ReasoningEffortMap["minimal"] != "low" ||
		flash.ReasoningEffortMap["medium"] != "high" ||
		flash.ReasoningEffortMap["xhigh"] != "max" {
		t.Fatalf("deepseek effort map = %+v", flash.ReasoningEffortMap)
	}
}

func TestProviderDefaultsFromCatalog(t *testing.T) {
	want := map[string]string{
		"openai":    "gpt-5.6-sol",
		"deepseek":  "deepseek-v4-flash",
		"anthropic": "claude-fable-5",
		"bytedance": "doubao-seed-evolving",
		"kimi":      "kimi-k3",
		"minimax":   "MiniMax-M3",
		"qwen":      "qwen3.8-max-preview",
		"azure":     "",
	}
	for id, name := range want {
		p, ok := ProviderByID(id)
		if !ok {
			t.Fatalf("provider %q missing", id)
		}
		if p.DefaultModel != name {
			t.Errorf("provider %s default = %q, want %q", id, p.DefaultModel, name)
		}
	}
}

// TestCatalogTablesMatchDriverCatalog guards the hand-maintained
// declaration-order and control-flag tables against driver catalog
// drift: every name they reference must still exist in the built
// catalog, or the next flowcraft upgrade fails here instead of
// silently picking wrong defaults.
func TestCatalogTablesMatchDriverCatalog(t *testing.T) {
	models, _, errs := providerCatalogs()
	for id, order := range catalogDefaultOrder {
		if errs[id] != "" {
			t.Fatalf("provider %s catalog failed: %s", id, errs[id])
		}
		names := make(map[string]bool, len(models[id]))
		for _, m := range models[id] {
			names[m.Name] = true
		}
		for _, name := range order {
			if !names[name] {
				t.Errorf(
					"catalogDefaultOrder[%s] references %q, missing from driver catalog",
					id, name)
			}
		}
	}
	for id, overrides := range catalogControlOverrides {
		if errs[id] != "" {
			t.Fatalf("provider %s catalog failed: %s", id, errs[id])
		}
		names := make(map[string]bool, len(models[id]))
		for _, m := range models[id] {
			names[m.Name] = true
		}
		for name := range overrides {
			if !names[name] {
				t.Errorf(
					"catalogControlOverrides[%s] references %q, missing from driver catalog",
					id, name)
			}
		}
	}
	if len(catalogControlOverrides["azure"]) != 0 {
		t.Error("azure must not have control overrides (no built-in catalog)")
	}
}
