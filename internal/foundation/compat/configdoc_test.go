package compat

import (
	"slices"
	"testing"
)

func TestShapeNeedsRewriteReportsRetiredShapes(t *testing.T) {
	canonical := []byte(`version: v1
resources:
  provider.openai-1:
    settings:
      spec:
        models:
          - name: 'gpt-test'
            kind: 'generate'
`)
	if got := ShapeNeedsRewrite(canonical); len(got) != 0 {
		t.Fatalf("canonical layer needs rewrite: %v", got)
	}

	stale := []byte(`version: v1
resources:
  provider.openai-1:
    settings:
      spec:
        models:
          - name: 'gpt-test'
            kind: 'generate'
            effort_none: true
`)
	got := ShapeNeedsRewrite(stale)
	if !slices.Equal(got, []string{"deprecated-model-keys"}) {
		t.Fatalf("stale layer shapes = %v, want [deprecated-model-keys]", got)
	}
}

// TestUserLayerShapesAreWellFormed pins the contract that adding a
// migration is one entry: every entry must be named and must carry the
// predicate that recognises it.
func TestUserLayerShapesAreWellFormed(t *testing.T) {
	seen := make(map[string]bool, len(UserLayerShapes))
	for _, shape := range UserLayerShapes {
		if shape.Name == "" {
			t.Fatal("user layer shape without a name")
		}
		if shape.Needs == nil {
			t.Fatalf("user layer shape %q without a predicate", shape.Name)
		}
		if seen[shape.Name] {
			t.Fatalf("user layer shape %q listed twice", shape.Name)
		}
		seen[shape.Name] = true
	}
}
