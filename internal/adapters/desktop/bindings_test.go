package desktop

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func TestModelOptionsStableIDsWithDisabledRow(t *testing.T) {
	dir := t.TempDir()
	cfg := config.InferenceConfig{Instances: []config.Instance{
		{StableID: "inst-a", Type: "deepseek", Name: "A", Models: []config.Model{{Name: "model-a"}}, KeySource: config.KeyEnv, Enabled: true},
		{StableID: "inst-b", Type: "deepseek", Models: []config.Model{{Name: "model-b"}}, KeySource: config.KeyEnv},
		{StableID: "inst-c", Type: "deepseek", Name: "C", Models: []config.Model{{Name: "model-c"}}, KeySource: config.KeyEnv, Enabled: true},
	}}
	if err := config.WriteInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	a := &App{userDir: dir}
	opts, err := a.ModelOptions()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"deepseek-inst-a/model-a", "deepseek-inst-c/model-c"}
	if len(opts) != len(want) {
		t.Fatalf("ModelOptions = %+v, want %v", opts, want)
	}
	for i, id := range want {
		if opts[i].ID != id {
			t.Fatalf("ModelOptions[%d].ID = %q, want %q (full: %+v)",
				i, opts[i].ID, id, opts)
		}
	}
	// The disabled row must not appear even though it sits between the
	// enabled ones in the written config.
	for _, o := range opts {
		if o.ID == "deepseek-inst-b/model-b" {
			t.Fatalf("disabled instance leaked into ModelOptions: %+v", opts)
		}
	}
}

func TestModelOptionsStableAcrossReorder(t *testing.T) {
	dir := t.TempDir()
	cfg := config.InferenceConfig{Instances: []config.Instance{
		{StableID: "inst-a", Type: "deepseek", Models: []config.Model{{Name: "model-a"}}, KeySource: config.KeyEnv, Enabled: true},
		{StableID: "inst-c", Type: "deepseek", Models: []config.Model{{Name: "model-c"}}, KeySource: config.KeyEnv, Enabled: true},
	}}
	if err := config.WriteInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	// Reorder and rewrite: the hint ids must not change.
	cfg.Instances[0], cfg.Instances[1] = cfg.Instances[1], cfg.Instances[0]
	if err := config.WriteInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	a := &App{userDir: dir}
	opts, err := a.ModelOptions()
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(opts))
	for _, o := range opts {
		seen[o.ID] = true
	}
	for _, want := range []string{"deepseek-inst-a/model-a", "deepseek-inst-c/model-c"} {
		if !seen[want] {
			t.Fatalf("hint %q lost after reorder: %+v", want, opts)
		}
	}
}
