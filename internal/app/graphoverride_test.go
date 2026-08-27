package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/deploy"
)

func TestNormalizeGraphOverride(t *testing.T) {
	doc := deploy.Document{
		Agents: map[string]agent.Definition{
			// A user override merged over the embedded default: both
			// source keys survive the deep merge.
			"assistant": {
				Engine: agent.EngineRef{
					Settings: json.RawMessage(
						`{"graph":{"embed":"assets/graphs/assistant.yaml",` +
							`"file":"graphs/my-assistant.yaml"},"stream":true}`),
				},
			},
			// The embedded default alone: single-key, untouched.
			"plain": {
				Engine: agent.EngineRef{
					Settings: json.RawMessage(
						`{"graph":{"embed":"assets/graphs/assistant.yaml"}}`),
				},
			},
		},
	}

	normalizeGraphOverride(doc)

	got := string(doc.Agents["assistant"].Engine.Settings)
	if !strings.Contains(got, `"graph":{"file":"graphs/my-assistant.yaml"}`) {
		t.Errorf("merged graph = %s, want reduced file reference", got)
	}
	if strings.Contains(got, "embed") {
		t.Errorf("merged graph = %s, want embed dropped", got)
	}
	if !strings.Contains(got, `"stream":true`) {
		t.Errorf("merged settings = %s, want sibling fields preserved", got)
	}
	if got := string(doc.Agents["plain"].Engine.Settings); got !=
		`{"graph":{"embed":"assets/graphs/assistant.yaml"}}` {
		t.Errorf("single-key graph mutated: %s", got)
	}
}
