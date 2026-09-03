package agents

import (
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/resource"
)

// toolAssemblyResource is the deploy-document resource name the
// dynamic catalog maps to the shared tool assembly. It must exist and
// must carry the opencraft tools (see dynamic_catalog in the embedded
// opencraft.yaml).
const toolAssemblyResource = "tools"

// agentDefinition assembles the agent.Definition for a persistent
// subagent: the caller-supplied graph definition is passed through as
// the graph engine's settings verbatim, and the worldstate prepare
// hook keeps the same basic context (workdir, permissions, skills) the
// main agent gets.
func agentDefinition(spec AgentSpec) agent.Definition {
	engineSettings, _ := json.Marshal(map[string]any{
		"graph": spec.Graph,
		"build": map[string]any{
			"timeout":        "1h",
			"max_iterations": 400,
		},
	})
	return agent.Definition{
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
