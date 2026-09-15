package config

import (
	"sort"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	coregraph "github.com/GizClaw/flowcraft/core/graph"
	graphnodes "github.com/GizClaw/flowcraft/core/graph/nodes"
	scriptnode "github.com/GizClaw/flowcraft/core/graph/nodes/script"
	"github.com/GizClaw/flowcraft/core/utils"
)

// TestAssistantGraphBuildWarnings pins the build-time findings of the
// shipped assistant graph. The analyzer runs on every runtime assembly
// and logs what it finds, so a finding that is not intended has to be
// fixed in the graph rather than tolerated at run time.
//
// The five remaining findings are the board variables the host seeds
// before the run (model/think_level/llm_extensions through
// agent.Request.Inputs, oc_thread_id/oc_turn_id through worldstate),
// which static analysis cannot see: GraphDefinition has no way to
// declare external inputs. Everything else must stay silent, including
// the split defaults (a missing default branch would mean a run can
// stop without an answer and without an error).
func TestAssistantGraphBuildWarnings(t *testing.T) {
	data, err := FS().ReadFile("assets/graphs/assistant.yaml")
	if err != nil {
		t.Fatalf("read assistant graph: %v", err)
	}
	def, err := utils.Decode[coregraph.GraphDefinition](data)
	if err != nil {
		t.Fatalf("decode assistant graph: %v", err)
	}
	reg := coregraph.NewRegistry()
	if err := graphnodes.RegisterInference(
		reg, graphnodes.InferenceNodeDeps{},
	); err != nil {
		t.Fatalf("register inference node type: %v", err)
	}
	if err := graphnodes.RegisterTool(reg, nil); err != nil {
		t.Fatalf("register tool node type: %v", err)
	}
	if err := scriptnode.Register(reg, scriptnode.ScriptNodeDeps{
		Runtimes: map[string]agent.ScriptRuntime{},
	}); err != nil {
		t.Fatalf("register script node type: %v", err)
	}
	built, err := coregraph.Build(&def, reg)
	if err != nil {
		t.Fatalf("build assistant graph: %v", err)
	}
	var got []string
	for _, w := range built.Warnings() {
		got = append(got, string(w.Kind)+"/"+w.NodeID)
	}
	want := []string{
		"unresolved_reference/llm",
		"unresolved_reference/llm",
		"unresolved_reference/llm",
		"unresolved_reference/llm",
		"unresolved_reference/llm",
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("assistant graph findings = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("assistant graph findings = %v, want %v", got, want)
		}
	}
}
