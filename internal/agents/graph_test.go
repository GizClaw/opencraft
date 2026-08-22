package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

func TestBuildGraphJSONIncludesInstructionsAndSurface(t *testing.T) {
	raw, err := buildGraphJSON(AgentSpec{
		Name:         "researcher",
		Instructions: "Summarize the architecture.\nBe concise.",
		Model:        "deepseek/deepseek-v4-flash",
		ThinkLevel:   "high",
		Tools:        ToolsReadOnly,
	})
	if err != nil {
		t.Fatalf("buildGraphJSON: %v", err)
	}
	var graph map[string]any
	if err := json.Unmarshal(raw, &graph); err != nil {
		t.Fatalf("graph is not valid JSON: %v", err)
	}
	if graph["name"] != "researcher" || graph["entry"] != "world" {
		t.Errorf("graph header = %v/%v", graph["name"], graph["entry"])
	}
	nodes, ok := graph["nodes"].([]any)
	if !ok || len(nodes) != 4 {
		t.Fatalf("nodes = %v, want 4", graph["nodes"])
	}
	var llm map[string]any
	for _, n := range nodes {
		node := n.(map[string]any)
		if node["id"] == "llm" {
			llm = node["config"].(map[string]any)
		}
	}
	if llm == nil {
		t.Fatal("no llm node")
	}
	prompt, _ := llm["system_prompt"].(string)
	if !strings.Contains(prompt, systemPromptPrefix) ||
		!strings.Contains(prompt, "Summarize the architecture.") {
		t.Errorf("system_prompt missing prefix/instructions")
	}
	if _, all := llm["all_tools"]; all {
		t.Error("read_only agent must not set all_tools")
	}
	tools, _ := llm["tools"].([]any)
	if len(tools) != len(readOnlyTools) {
		t.Errorf("read_only tools = %v", tools)
	}
	intent := llm["intent"].(map[string]any)["text"].(map[string]any)
	if intent["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", intent["reasoning_effort"])
	}
	model := llm["model"].(map[string]any)["id"].(map[string]any)
	if model["provider"] != "deepseek" || model["name"] != "deepseek-v4-flash" {
		t.Errorf("model = %v", model)
	}
}

func TestBuildGraphJSONAllToolsByDefault(t *testing.T) {
	raw, err := buildGraphJSON(AgentSpec{
		Name:         "worker",
		Instructions: "do it",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"all_tools":true`) {
		t.Errorf("default graph should set all_tools: %s", raw)
	}
}

func TestGraphDefinitionValidate(t *testing.T) {
	def, err := graphDefinition(AgentSpec{
		Name:         "worker",
		Description:  "desc",
		Instructions: "do it",
		Model:        "deepseek/deepseek-v4-flash",
		ThinkLevel:   "medium",
	})
	if err != nil {
		t.Fatalf("graphDefinition: %v", err)
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	if def.Card.Name != "worker" || def.Card.Description != "desc" {
		t.Errorf("card = %+v", def.Card)
	}
	if len(def.Prepare) != 1 || def.Prepare[0].Type != "opencraft.prepare" {
		t.Errorf("prepare = %+v", def.Prepare)
	}
}

func TestGraphDefinitionRejectsBadModel(t *testing.T) {
	if _, err := graphDefinition(AgentSpec{
		Name:         "worker",
		Description:  "desc",
		Instructions: "do it",
		Model:        "no-slash",
	}); err == nil {
		t.Fatal("bad model accepted")
	} else if !errdefs.IsValidation(err) {
		t.Errorf("error = %v, want validation", err)
	}
}
