package agents

import (
	"encoding/json"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/resource"
)

// toolAssemblyResource is the deploy-document resource name the
// dynamic catalog maps to the shared tool assembly. It must exist and
// must carry the opencraft tools (see dynamic_catalog in the embedded
// opencraft.yaml).
const toolAssemblyResource = "tools"

// readOnlyTools is the tool surface of a read_only subagent: no edits,
// no commands.
var readOnlyTools = []string{
	"read_file", "grep", "glob", "list_dir", "web_fetch",
}

// systemPromptPrefix is the concise general preamble prepended to a
// subagent's instructions: it teaches tool behavior and reporting
// without duplicating the full assistant prompt.
const systemPromptPrefix = `You are a specialized subagent created by opencraft. You run on the
same flowcraft graph engine as the main agent and complete one focused
assignment, then report back.

## Working style
- Complete the assigned task using the workspace tools; prefer ` + "`rg`" + ` or
  ` + "`rg --files`" + ` when searching.
- Issue independent reads in the same round as multiple tool calls
  instead of one at a time; do not re-read a file after editing it.
- Do not start unrelated work or expand scope beyond the assignment.

## Reporting
- Return a concise result: what you did, key findings or artifacts, and
  any blockers. Your output goes back to the delegating agent, not the
  user.

## Assignment
`

// graphDefinition assembles the agent.Definition of a persistent
// subagent: the graph engine with a generated single-loop graph whose
// system prompt is the shared prefix plus the caller's instructions.
func graphDefinition(spec AgentSpec) (agent.Definition, error) {
	graphJSON, err := buildGraphJSON(spec)
	if err != nil {
		return agent.Definition{}, err
	}
	engineSettings, err := json.Marshal(map[string]any{
		"graph": json.RawMessage(graphJSON),
		"build": map[string]any{
			"timeout":        "1h",
			"max_iterations": 400,
		},
	})
	if err != nil {
		return agent.Definition{}, err
	}
	def := agent.Definition{
		Card: agent.AgentCard{
			Name:        spec.Name,
			Description: spec.Description,
		},
		Engine: agent.EngineRef{
			Kind: "agent.Engine",
			Impl: "graph",
			Deps: resource.Deps{
				"inference":      "infer",
				"router":         "router",
				"tools":          "tools",
				"workspace":      "ws",
				"sandbox":        "box",
				"script_runtime": "js",
			},
			Settings: engineSettings,
		},
		Prepare: []agent.Hook{prepareHook()},
	}
	return def, def.Validate()
}

// mustGraphDefinition is LoadAll's variant: the spec already passed
// validation, so graph building cannot fail.
func mustGraphDefinition(spec AgentSpec) agent.Definition {
	def, err := graphDefinition(spec)
	if err != nil {
		panic(err)
	}
	return def
}

// prepareHook mirrors the assistant's worldstate hook so subagents
// receive the same basic context (workdir, permissions, skills).
func prepareHook() agent.Hook {
	settings, _ := json.Marshal(map[string]string{
		"work_dir":           "${env:OPEN_CRAFT_WORKDIR}",
		"user_dir":           "${env:OPEN_CRAFT_DATA_DIR}",
		"collaboration_mode": "default",
		"permission_profile": "workspace",
	})
	return agent.Hook{
		Type: "opencraft.prepare",
		Deps: resource.Deps{
			"memory":     "mem",
			"workspace":  "ws",
			"execpolicy": "execpolicy",
			"sessions":   "sessions",
			"skills":     "skills",
		},
		Settings: settings,
	}
}

// buildGraphJSON renders the graph definition as JSON (a strict subset
// of the YAML the graph engine decodes). It mirrors the embedded
// assistant graph, substituting the subagent's system prompt, optional
// model, reasoning effort, and tool surface.
func buildGraphJSON(spec AgentSpec) ([]byte, error) {
	thinkLevel := spec.ThinkLevel
	if thinkLevel == "" {
		thinkLevel = "medium"
	}
	llmConfig := map[string]any{
		"tool_pending_key": "tool_pending",
		"stream":           true,
		"system_prompt":    systemPromptPrefix + spec.Instructions,
		"intent": map[string]any{
			"text": map[string]any{
				"reasoning_effort": thinkLevel,
			},
		},
		"extensions": []map[string]any{
			{
				"provider": "deepseek",
				"id":       "generate_options",
				"fields": map[string]any{
					"web_search": map[string]any{"tool_choice": map[string]any{"required": false}},
				},
			},
			{
				"provider": "openai",
				"id":       "generate_options",
				"fields": map[string]any{
					"web_search": map[string]any{"tool_choice": map[string]any{"required": false}},
				},
			},
			{
				"provider": "bytedance",
				"id":       "generate_options",
				"fields":   map[string]any{"web_search": map[string]any{}},
			},
		},
	}
	switch spec.Tools {
	case "", ToolsAll:
		llmConfig["all_tools"] = true
	case ToolsReadOnly:
		llmConfig["tools"] = readOnlyTools
	}
	if spec.Model != "" {
		provider, name, _ := strings.Cut(spec.Model, "/")
		model := inference.ModelRef{ID: inference.ModelID{
			Provider: provider,
			Name:     name,
		}}
		if err := model.Validate(); err != nil {
			return nil, errdefs.Validationf(
				"agents: model %q: %v", spec.Model, err)
		}
		llmConfig["model"] = model
	}

	graph := map[string]any{
		"name":  spec.Name,
		"entry": "world",
		"nodes": []map[string]any{
			{
				"id":   "world",
				"type": "script",
				"config": map[string]any{
					"runtime": "js",
					"source":  map[string]any{"embed": "assets/graphs/nodes/world.js"},
				},
			},
			{
				"id":   "compact",
				"type": "script",
				"config": map[string]any{
					"runtime": "js",
					"source":  map[string]any{"embed": "assets/graphs/nodes/compact.js"},
					"config": map[string]any{
						"preserve_recent":      10,
						"budget_chars":         4096,
						"threshold_ratio":      0.85,
						"max_compactions":      3,
						"max_input_tokens":     0,
						"system_prompt_tokens": 2000,
					},
				},
			},
			{
				"id":     "llm",
				"type":   "inference",
				"config": llmConfig,
			},
			{
				"id":   "tools",
				"type": "tool",
				"config": map[string]any{
					"results_key": "tool_results",
				},
			},
		},
		"edges": []map[string]any{
			{"from": "world", "to": "compact"},
			{"from": "compact", "to": "llm", "condition": "tool_pending == false"},
			{"from": "compact", "to": "tools", "condition": "tool_pending == true"},
			{"from": "llm", "to": "tools", "condition": "tool_pending == true"},
			{"from": "llm", "to": "__end__", "condition": "tool_pending == false"},
			{"from": "tools", "to": "compact"},
		},
	}
	return json.Marshal(graph)
}
