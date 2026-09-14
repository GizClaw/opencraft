package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"
)

func providerSettings(t *testing.T, body string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(body)) {
		t.Fatalf("test settings are not valid JSON: %s", body)
	}
	return json.RawMessage(body)
}

// TestVideoHintsPinsWireAndModelGate: a hint is listed only when both
// the endpoint accepts video blocks and the model declares video input,
// and the router's default target is listed as "" so turns without a
// hint still resolve.
func TestVideoHintsPinsWireAndModelGate(t *testing.T) {
	doc := deploy.Document{Resources: resource.Resources{
		"provider.kimi-1": {
			Kind: "inference.Provider",
			Impl: "openai",
			Settings: providerSettings(t, `{
				"id": "kimi-1",
				"spec": {
					"api": "chat",
					"wire": {"video_input": true},
					"models": [
						{"name": "kimi-k3", "capabilities": {"inputs": ["text", "video"]}},
						{"name": "moonshot-v1-8k", "capabilities": {"inputs": ["text"]}}
					]
				}
			}`),
		},
		// Declares video input but the endpoint does not accept it: the
		// driver would reject the part, so the hook must keep flattening.
		"provider.openai-1": {
			Kind: "inference.Provider",
			Impl: "openai",
			Settings: providerSettings(t, `{
				"id": "openai-1",
				"spec": {
					"api": "chat",
					"models": [{"name": "glm-5.3-flash", "capabilities": {"inputs": ["video"]}}]
				}
			}`),
		},
		// Responses surface: no video lowering even with the flag set.
		"provider.deepseek-1": {
			Kind: "inference.Provider",
			Impl: "openai",
			Settings: providerSettings(t, `{
				"id": "deepseek-1",
				"spec": {
					"api": "responses",
					"wire": {"video_input": true},
					"models": [{"name": "deepseek-v4-flash", "capabilities": {"inputs": ["video"]}}]
				}
			}`),
		},
		"router": {
			Kind: "inference.Router",
			Settings: providerSettings(t, `{
				"generate": [{"tier": "default", "targets": [
					{"model": {"id": {"provider": "kimi-1", "name": "kimi-k3"}}}
				]}]
			}`),
		},
		"agent.hooks": {
			Kind:     "hook.prepare",
			Impl:     "opencraft.media",
			Settings: providerSettings(t, `{"work_dir": "/tmp/w"}`),
		},
	}}

	hints := videoHints(doc)
	if len(hints) != 2 || hints[0] != "" || hints[1] != "kimi-1/kimi-k3" {
		t.Fatalf("hints = %v, want the default entry plus the explicit hint", hints)
	}

	patched, err := withVideoHints(doc)
	if err != nil {
		t.Fatal(err)
	}
	settings := string(patched.Resources["agent.hooks"].Settings)
	if !strings.Contains(settings, `"video_models":["","kimi-1/kimi-k3"]`) {
		t.Fatalf("media hook settings = %s", settings)
	}
	if !strings.Contains(settings, `"work_dir":"/tmp/w"`) {
		t.Fatalf("media hook lost its own settings: %s", settings)
	}
}

// TestVideoHintsKeepNonDefaultTargets: a video-capable target that is
// not the router's first one is listed by its own hint.
func TestVideoHintsKeepNonDefaultTargets(t *testing.T) {
	doc := deploy.Document{Resources: resource.Resources{
		"provider.kimi-1": {
			Kind: "inference.Provider",
			Impl: "openai",
			Settings: providerSettings(t, `{
				"id": "kimi-1",
				"spec": {
					"api": "chat",
					"wire": {"video_input": true},
					"models": [{"name": "kimi-k3", "capabilities": {"inputs": ["video"]}}]
				}
			}`),
		},
		"router": {
			Kind: "inference.Router",
			Settings: providerSettings(t, `{
				"generate": [{"tier": "default", "targets": [
					{"model": {"id": {"provider": "openai-1", "name": "gpt-5.6-sol"}}}
				]}]
			}`),
		},
	}}
	hints := videoHints(doc)
	if len(hints) != 1 || hints[0] != "kimi-1/kimi-k3" {
		t.Fatalf("hints = %v", hints)
	}
}
