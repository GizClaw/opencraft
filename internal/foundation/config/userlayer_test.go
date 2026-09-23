package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestUserLayerWritersLeaveNoEmptySettings pins the invariant every
// user-layer writer holds: a resource entry either carries a settings
// object with at least one key, or says nothing about settings at all.
//
// Why it matters: flowcraft reads a resource's settings subtree as a
// whole-subtree deploy source and rejects its empty spellings —
// `settings: {}` is "resource source: empty object is not valid", a
// bare or null value is "scalar values must be JSON strings" — by
// failing the build of the whole document, so one such entry leaves
// every workspace unassemblable. A settings struct whose fields are all
// omitempty marshals to `{}` on a zero-value save, which is exactly how
// the delegation policy resource broke. The writers below are the ones
// whose settings shape can marshal empty; each is saved with the zero
// value, the case that used to write the empty object.
func TestUserLayerWritersLeaveNoEmptySettings(t *testing.T) {
	cases := []struct {
		name  string
		write func(dir string) error
	}{
		{"delegation", func(dir string) error {
			return SaveDelegation(dir, DelegationSettings{})
		}},
		{"skill lifecycle", func(dir string) error {
			return SaveSkillLifecycle(dir, SkillLifecycleSettings{})
		}},
		{"user memory", func(dir string) error {
			return SaveUserMemory(dir, UserMemorySettings{})
		}},
		{"review", func(dir string) error {
			return SaveReview(dir, ReviewSettings{})
		}},
		{"web search", func(dir string) error {
			return SaveWebSearch(dir, WebSearchSettings{})
		}},
		{"memory", func(dir string) error {
			return WriteMemory(dir, MemorySettings{})
		}},
		{"tool options", func(dir string) error {
			return SaveToolOptions(dir, ToolOptions{}, nil)
		}},
		{"mcp", func(dir string) error {
			return WriteMCP(dir, nil)
		}},
		{"inference", func(dir string) error {
			_, err := UpdateInferenceState(dir, func(
				InferenceConfig, map[string]string,
			) (InferenceConfig, map[string]string, bool, error) {
				return InferenceConfig{Instances: []Instance{{
					Type:      Providers[0].ID,
					KeySource: KeyEnv,
					Enabled:   true,
					Models:    []Model{{Name: "test-model"}},
				}}}, nil, true, nil
			})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := tc.write(dir); err != nil {
				t.Fatalf("write: %v", err)
			}
			assertNoEmptySettings(t, filepath.Join(dir, "opencraft.yaml"))
			// The layer the writer produced still merges into a document
			// flowcraft accepts: an empty object is not the only way to
			// leave a resource without a readable source, so the merge
			// itself is part of the invariant.
			assertLayerMerges(t, dir)
		})
	}
}

// assertNoEmptySettings fails when any resource of the layer carries a
// settings key that deploy cannot read.
func assertNoEmptySettings(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read user layer: %v", err)
	}
	var doc struct {
		Resources map[string]map[string]any `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse user layer: %v", err)
	}
	for name, resource := range doc.Resources {
		settings, ok := resource["settings"]
		if !ok {
			continue
		}
		mapping, isMapping := settings.(map[string]any)
		if !isMapping || len(mapping) == 0 {
			t.Errorf(
				"resource %q carries settings %#v, which deploy rejects:\n%s",
				name, settings, data)
		}
	}
}

// assertLayerMerges fails when the written layer does not merge with
// the embedded base into a valid deploy document.
func assertLayerMerges(t *testing.T, dir string) {
	t.Helper()
	mgr, err := Open(Options{UserDir: dir})
	if err != nil {
		t.Fatalf("open config: %v", err)
	}
	if _, err := mgr.Load(context.Background()); err != nil {
		t.Fatalf("merged document: %v", err)
	}
}
