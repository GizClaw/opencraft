package engine

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// webSearchCase is one driver whose deployment must accept the hosted
// web_search extension emitted by config.WebSearchExtensions.
type webSearchCase struct {
	name   string
	envVar string
	model  config.Model
	api    string
}

// webSearchDrivers covers every catalog provider whose driver exposes
// a hosted web_search generate option. Models are declared exactly as
// the settings page would after enabling the checkbox on a
// search-capable catalog model.
func webSearchDrivers() []webSearchCase {
	hosted := func(name string) config.Model {
		return config.Model{
			Name: name,
			Capabilities: inference.ModelCapabilities{
				HostedWebSearch: true,
			},
		}
	}
	return []webSearchCase{
		{
			name:   "deepseek",
			envVar: "DEEPSEEK_API_KEY",
			api:    "responses",
			model:  hosted("deepseek-v4-flash"),
		},
		{
			name:   "openai",
			envVar: "OPENAI_API_KEY",
			api:    "responses",
			model:  hosted("gpt-5.6-sol"),
		},
		{
			name:   "bytedance",
			envVar: "ARK_API_KEY",
			model:  hosted("doubao-seed-2-1-pro"),
		},
		{
			name:   "azure",
			envVar: "AZURE_OPENAI_API_KEY",
			model: config.Model{
				Name: "gpt-5.6-sol-deploy",
				Kind: "generate",
				Capabilities: inference.ModelCapabilities{
					HostedWebSearch: true,
				},
			},
		},
	}
}

// buildWebSearchRuntime assembles a full runtime from one enabled
// inference deployment.
func buildWebSearchRuntime(
	t *testing.T,
	inst config.Instance,
) (*ocsessions.Store, *inference.Assembly, config.InferenceConfig) {
	t.Helper()
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	cfg := config.InferenceConfig{Instances: []config.Instance{inst}}
	if err := config.WriteInference(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}

	mgr, err := config.Open(config.Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	layout := testWorkspaceLayout(t, home, work)
	sessionStore := migratedSessionStore(t, layout)
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithWorkspaceLayout(layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return sessionStore, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	assemblyValue, ok := rt.Resource("infer")
	if !ok {
		t.Fatal("infer assembly resource missing")
	}
	assembly, ok := assemblyValue.(*inference.Assembly)
	if !ok || assembly == nil {
		t.Fatalf("infer resource is %T, want *inference.Assembly", assemblyValue)
	}
	return sessionStore, assembly, cfg
}

// explainWebSearch decodes the board bag against a live runtime's
// extension decoders and explains one generate against the deployment.
// A Native decision on the web_search field proves the extension
// survives decode and the deployment-id filter all the way into the
// driver compiler (the same pipeline the board-seeded graph runs).
func explainWebSearch(
	t *testing.T,
	assembly *inference.Assembly,
	cfg config.InferenceConfig,
	deploymentID, modelName, profile string,
) inference.Explanation {
	t.Helper()
	raw, err := json.Marshal(cfg.WebSearchExtensions())
	if err != nil {
		t.Fatal(err)
	}
	var wire []inference.ExtensionEntry
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	extensions, err := inference.DecodeExtensions(
		wire, assembly.ExtensionDecoders(), "web search board bag")
	if err != nil {
		t.Fatalf("decode board bag: %v", err)
	}

	ref := inference.ModelRef{
		ID: inference.ModelID{
			Provider: deploymentID,
			Name:     modelName,
		},
		Profile: profile,
	}
	req := inference.GenerateRequest{Input: inference.GenerateInput{
		Role: inference.InputRoleUser,
		Content: inference.InputContent{
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "hello"},
			}},
			Intent: inference.Intent{Text: &inference.TextIntent{}},
		},
	}}
	req.Extensions = extensions
	explained, err := assembly.ExplainGenerate(context.Background(), ref, req)
	if err != nil {
		t.Fatalf("explain generate: %v", err)
	}
	return explained
}

// assertWebSearchNative scans an explanation for the deployment's
// web_search compile decision.
func assertWebSearchNative(
	t *testing.T,
	explained inference.Explanation,
	deploymentID string,
) {
	t.Helper()
	prefix := "extension." + deploymentID + ".generate_options.web_search"
	for _, decision := range explained.Decisions {
		if !strings.HasPrefix(string(decision.Field), prefix) {
			continue
		}
		if decision.Disposition == inference.Native {
			return
		}
		t.Fatalf("web_search decision = %+v, want native", decision)
	}
	t.Fatalf("no web_search decision in %+v", explained.Decisions)
}

func TestHostedWebSearchBoardBagReachesEveryDriverCompiler(t *testing.T) {
	for _, tc := range webSearchDrivers() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envVar, "test-key")
			inst := config.Instance{
				StableID:  "inst-aaa",
				Type:      tc.name,
				API:       tc.api,
				KeySource: config.KeyEnv,
				Enabled:   true,
				Models:    []config.Model{tc.model},
			}
			if tc.name == "azure" {
				inst.Endpoint = "https://oc-test.openai.azure.com"
			}
			_, assembly, cfg := buildWebSearchRuntime(t, inst)
			entries := cfg.WebSearchExtensions()
			if len(entries) != 1 {
				t.Fatalf("web search entries = %+v, want one", entries)
			}
			deploymentID := tc.name + "-inst-aaa"
			if entries[0].Provider != deploymentID {
				t.Fatalf("entry provider = %q, want %q", entries[0].Provider, deploymentID)
			}
			explained := explainWebSearch(
				t, assembly, cfg, deploymentID, tc.model.Name, "inst-aaa")
			assertWebSearchNative(t, explained, deploymentID)
		})
	}
}

// TestHostedWebSearchRejectsUnsupportedModel documents the driver-side
// hard rejection that motivates the config gate: an extension emitted
// for a deployment whose selected model does not declare hosted web
// search fails the compile instead of degrading silently.
func TestHostedWebSearchRejectsUnsupportedModel(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	plain := config.Model{Name: "deepseek-v4-pro"}
	inst := config.Instance{
		StableID:  "inst-aaa",
		Type:      "deepseek",
		API:       "responses",
		KeySource: config.KeyEnv,
		Enabled:   true,
		Models:    []config.Model{plain},
	}
	_, assembly, cfg := buildWebSearchRuntime(t, inst)
	if entries := cfg.WebSearchExtensions(); len(entries) != 0 {
		t.Fatalf("plain deployment must not emit extensions: %+v", entries)
	}

	// Emulating a per-model mistake — an extension addressed to the
	// deployment while the selected model lacks the capability — must
	// fail the compile with the driver's rejection.
	rawEntry, err := json.Marshal([]config.HostedWebSearchExtension{{
		Provider: "deepseek-inst-aaa",
		ID:       "generate_options",
		Fields: map[string]any{
			"web_search": map[string]any{
				"tool_choice": map[string]any{"required": false},
			},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var entry []inference.ExtensionEntry
	if err := json.Unmarshal(rawEntry, &entry); err != nil {
		t.Fatal(err)
	}
	extensions, err := inference.DecodeExtensions(
		entry, assembly.ExtensionDecoders(), "unsupported model extension")
	if err != nil {
		t.Fatal(err)
	}
	ref := inference.ModelRef{
		ID: inference.ModelID{
			Provider: "deepseek-inst-aaa",
			Name:     "deepseek-v4-pro",
		},
		Profile: "inst-aaa",
	}
	req := inference.GenerateRequest{Input: inference.GenerateInput{
		Role: inference.InputRoleUser,
		Content: inference.InputContent{
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "hello"},
			}},
			Intent: inference.Intent{Text: &inference.TextIntent{}},
		},
	}}
	req.Extensions = extensions
	_, err = assembly.ExplainGenerate(context.Background(), ref, req)
	var infErr *inference.Error
	if !errors.As(err, &infErr) ||
		infErr.Kind != inference.InvalidExtension ||
		!strings.HasSuffix(string(infErr.Field), ".web_search") {
		t.Fatalf("explain = %v, want invalid_extension rejection on web_search", err)
	}
}
