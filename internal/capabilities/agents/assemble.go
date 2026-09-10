package agents

import (
	"context"
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
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
// the graph engine's settings, and the worldstate prepare hook keeps
// the same basic context (workdir, permissions, skills) the main
// agent gets. The hook paths are the assembly values the lifecycle
// received at build time (see Settings); the definition must not
// carry ${...} references because runtime.RegisterAgent expands agent
// settings without the builder's custom resolver. Callers validate the
// spec first, so the error path is defensive: a malformed declaration
// must fail registration instead of being registered half-built.
func (l *Lifecycle) agentDefinition(spec AgentSpec) (agent.Definition, error) {
	graph, err := decodeGraphOnly(spec.Engine.Settings)
	if err != nil {
		return agent.Definition{}, errdefs.Validationf(
			"agents: decode graph settings: %v", err)
	}
	engineSettings, err := json.Marshal(map[string]any{
		"graph": graph,
		"build": map[string]any{
			"timeout": "1h",
			// Loop guard counts node invocations, and this graph spends
			// about three nodes per tool round, so 2000 leaves room for
			// long-horizon subagent work while the 1h build timeout still
			// bounds wall clock.
			"max_iterations": 2000,
		},
	})
	if err != nil {
		return agent.Definition{}, errdefs.Internalf(
			"agents: marshal engine settings: %v", err)
	}
	return agent.Definition{
		Card: spec.Card,
		Engine: agent.EngineRef{
			Kind: spec.Engine.Kind,
			Impl: spec.Engine.Impl,
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
	}, nil
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
