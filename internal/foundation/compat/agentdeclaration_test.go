package compat

import (
	"testing"
	"time"
)

func TestLegacyAgentDeclarationDecodesFlatRecord(t *testing.T) {
	decl, ok, err := LegacyAgentDeclaration([]byte(`
name: researcher
description: old shape
graph: |
  nodes:
    - id: llm
created_at: 2026-08-27T10:18:54Z
`))
	if err != nil || !ok {
		t.Fatalf("legacy declaration: ok=%v err=%v", ok, err)
	}
	if decl.Name != "researcher" || decl.Description != "old shape" {
		t.Fatalf("decoded declaration = %+v", decl)
	}
	if decl.Graph != "nodes:\n  - id: llm\n" {
		t.Fatalf("decoded graph = %q", decl.Graph)
	}
	if !decl.CreatedAt.Equal(time.Date(2026, 8, 27, 10, 18, 54, 0, time.UTC)) {
		t.Fatalf("decoded created_at = %s", decl.CreatedAt)
	}
}

// TestLegacyAgentDeclarationRejectsCurrentShape pins the discriminator:
// the versioned format stores its graph under engine.settings, so a
// document carrying card or version is current even when a top-level
// graph string is present.
func TestLegacyAgentDeclarationRejectsCurrentShape(t *testing.T) {
	for name, doc := range map[string]string{
		"card": `
card:
  name: researcher
engine:
  settings:
    graph:
      nodes: []
version: 1
`,
		"version only": `
version: 1
graph: "nodes: []"
`,
		"no graph": `
name: researcher
description: current shape without a graph field
`,
	} {
		decl, ok, err := LegacyAgentDeclaration([]byte(doc))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if ok {
			t.Fatalf("%s: decoded as legacy: %+v", name, decl)
		}
	}
}

// TestLegacyAgentDeclarationRejectsUnknownFields keeps the strict
// decode: a flat record cannot smuggle host-owned wiring through the
// legacy path.
func TestLegacyAgentDeclarationRejectsUnknownFields(t *testing.T) {
	_, ok, err := LegacyAgentDeclaration([]byte(`
name: researcher
description: old shape
graph: "nodes: []"
build:
  model: injected
`))
	if err == nil || ok {
		t.Fatalf("unknown field: ok=%v err=%v, want a strict-decode error", ok, err)
	}
}
