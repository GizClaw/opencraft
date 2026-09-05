// Hosted web search board extensions.
//
// Flowcraft addresses provider generate_options extensions by the
// deployment provider id (settings.id), not by the driver type: an
// extension entry only applies on the attempt whose selected model
// belongs to that exact deployment. A shared static graph cannot know
// the deployment ids opencraft mints per instance, so this file lowers
// an InferenceConfig into the per-run extension bag the host seeds on
// the assistant board (board:llm_extensions). The default graph
// consumes the bag verbatim; entries whose deployment is not selected
// are inert and never rejected.
//
// One bag entry is emitted per enabled instance whose generate models
// all declare hosted web search and whose generate surface accepts it
// (responses for OpenAI/DeepSeek; ByteDance and Azure have no chat
// split). Per-model enablement inside one instance cannot be expressed:
// flowcraft extensions have provider granularity, so a mixed instance
// (searchable and non-searchable generate models) is skipped wholesale
// to keep the non-searchable models compilable.
package config

// HostedWebSearchExtension is the wire form of one provider
// generate_options extension the graph should attach. It mirrors
// flowcraft's extension entry shape ({provider, id, fields}) so the
// host can seed it onto the board without importing driver packages.
type HostedWebSearchExtension struct {
	Provider string         `json:"provider"`
	ID       string         `json:"id"`
	Fields   map[string]any `json:"fields"`
}

// hostedWebSearchExtensionID is the flowcraft generate_options
// extension id that carries the provider web_search knob.
const hostedWebSearchExtensionID = "generate_options"

// webSearchCapableProviders lists the catalog provider types whose
// drivers expose a hosted web_search generate option. Anthropic, Kimi,
// MiniMax, and Qwen do not, so a manually enabled checkbox on their
// models cannot lower onto the wire and is never emitted.
var webSearchCapableProviders = map[string]bool{
	"openai":    true,
	"deepseek":  true,
	"bytedance": true,
	"azure":     true,
}

// generateModelKind reports whether a model is a generate-surface
// candidate (text generation is the writer's default family; embed /
// image / video / tts models never carry the search tool).
func generateModelKind(kind string) bool {
	switch kind {
	case "embed", "image", "video", "tts":
		return false
	default:
		return true
	}
}

// WebSearchExtensions lowers the enabled instances into the hosted
// web_search extension bag for the assistant graph. The returned list
// is empty when no deployment can safely carry the knob.
func (c InferenceConfig) WebSearchExtensions() []HostedWebSearchExtension {
	var out []HostedWebSearchExtension
	for i, in := range c.Instances {
		if !in.Enabled || !webSearchCapableProviders[in.Type] {
			continue
		}
		if len(in.Models) == 0 {
			continue
		}
		// OpenAI/DeepSeek expose web_search only on the Responses
		// surface; the chat compiler rejects the knob outright.
		if in.Type == "openai" || in.Type == "deepseek" {
			api := in.API
			if api == "" {
				if prov, ok := ProviderByID(in.Type); ok {
					api = prov.API
				}
			}
			if api == "chat" {
				continue
			}
		}
		searchable := false
		for _, m := range in.Models {
			if !generateModelKind(m.Kind) {
				continue
			}
			if !m.Capabilities.HostedWebSearch {
				searchable = false
				break
			}
			searchable = true
		}
		if !searchable {
			continue
		}
		// The deployment id mirrors InferenceYAML: a stable identity is
		// authoritative. Legacy instances without one get
		// position-derived ids ("<type>-<n>"), which only match the
		// writer when the config carries no disabled instances:
		// LoadInference reorders disabled rows after the enabled ones,
		// so their presence breaks the positional fallback. Emitting a
		// wrong id would reference a provider the runtime never
		// registered and fail every decode, so such rows are skipped.
		id := in.DeploymentID(i + 1)
		if in.StableID == "" {
			hasDisabled := false
			for j := range c.Instances {
				if !c.Instances[j].Enabled {
					hasDisabled = true
					break
				}
			}
			if hasDisabled {
				continue
			}
		}
		out = append(out, HostedWebSearchExtension{
			Provider: id,
			ID:       hostedWebSearchExtensionID,
			Fields:   webSearchFields(in.Type),
		})
	}
	return out
}

// webSearchFields returns the generate_options fields each driver's
// web_search schema accepts. OpenAI/DeepSeek/Azure share the
// tool_choice knob (required=false lets the model decide when to
// search); ByteDance's schema has no tool_choice, so it stays empty.
func webSearchFields(providerType string) map[string]any {
	if providerType == "bytedance" {
		return map[string]any{
			"web_search": map[string]any{},
		}
	}
	return map[string]any{
		"web_search": map[string]any{
			"tool_choice": map[string]any{
				"required": false,
			},
		},
	}
}
