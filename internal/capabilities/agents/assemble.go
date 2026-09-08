package agents

import (
	"context"
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
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
// main agent gets. The hook paths are the assembly values the
// lifecycle received at build time (see Settings); the definition must
// not carry ${...} references because runtime.RegisterAgent expands
// agent settings without the builder's custom resolver.
func (l *Lifecycle) agentDefinition(spec AgentSpec) agent.Definition {
	engineSettings, err := json.Marshal(map[string]any{
		"graph": spec.Graph,
		"build": map[string]any{
			"timeout":        "1h",
			"max_iterations": 400,
		},
	})
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"agents: marshal engine settings failed", err)
	}
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
		Prepare: []agent.Hook{l.prepareHook()},
	}
}

// prepareHook mirrors the assistant's worldstate hook so subagents
// receive the same basic context (workdir, permissions, skills).
func (l *Lifecycle) prepareHook() agent.Hook {
	settings, err := json.Marshal(map[string]string{
		"work_dir":           l.work,
		"user_dir":           l.user,
		"collaboration_mode": "default",
		"permission_profile": "workspace",
	})
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"agents: marshal prepare hook settings failed", err)
	}
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
