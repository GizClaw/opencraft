package config

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/driver/anthropic"
	"github.com/GizClaw/flowcraft/driver/bytedance"
	"github.com/GizClaw/flowcraft/driver/deepseek"
	"github.com/GizClaw/flowcraft/driver/kimi"
	"github.com/GizClaw/flowcraft/driver/minimax"
	"github.com/GizClaw/flowcraft/driver/openai"
	"github.com/GizClaw/flowcraft/driver/qwen"
)

// ModelTemplate is one built-in driver catalog model, normalized for
// the settings page. It carries everything opencraft needs to prefill
// a model row: capabilities, the canonical-to-wire effort map, and
// driver control flags.
type ModelTemplate struct {
	Name               string            `json:"name"`
	Kind               string            `json:"kind"`
	Inputs             []string          `json:"inputs"`
	Outputs            []string          `json:"outputs"`
	Reasoning          string            `json:"reasoning"`
	ReasoningEffortMap map[string]string `json:"reasoning_effort_map,omitempty"`
	WebSearch          bool              `json:"web_search"`
	Dimensions         bool              `json:"dimensions,omitempty"`
	EffortNone         bool              `json:"effort_none,omitempty"`
	Deprecated         bool              `json:"deprecated"`
	Replacement        string            `json:"replacement,omitempty"`
	MaxInputTokens     *int              `json:"max_input_tokens,omitempty"`
}

// ProviderModels is one provider's built-in catalog for the settings
// page dropdown.
type ProviderModels struct {
	Provider string          `json:"provider"`
	Models   []ModelTemplate `json:"models"`
	// Error carries a per-provider catalog build failure so the
	// settings page can degrade to manual input instead of failing
	// the whole config load.
	Error string `json:"error,omitempty"`
}

// catalogControlFlags mirrors driver built-in control flags that are
// not exposed on inference.ModelDescriptor.
type catalogControlFlags struct {
	Dimensions bool
	EffortNone bool
}

// catalogControlOverrides documents the v0.2.1 driver built-in
// dimensions / effort_none flags (openai only; azure has no built-in
// catalog). These are editor prefill facts only; the deployed config
// is still validated by the driver.
var catalogControlOverrides = map[string]map[string]catalogControlFlags{
	"openai": {
		"gpt-5.6-sol":            {EffortNone: true},
		"gpt-5.6-terra":          {EffortNone: true},
		"gpt-5.6-luna":           {EffortNone: true},
		"text-embedding-3-small": {Dimensions: true},
		"text-embedding-3-large": {Dimensions: true},
	},
}

// catalogDefaultOrder preserves each driver catalog's source
// declaration order (the driver's own recommendation order), so
// "first non-deprecated model" is deterministic and meaningful.
var catalogDefaultOrder = map[string][]string{
	"openai": {
		"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5",
		"gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-4.1",
		"gpt-4.1-mini", "gpt-4.1-nano", "text-embedding-3-small",
		"text-embedding-3-large", "text-embedding-ada-002", "gpt-image-2",
		"gpt-image-1", "gpt-4o-mini-tts", "tts-1", "tts-1-hd",
	},
	"deepseek": {
		"deepseek-v4-flash", "deepseek-v4-pro",
		"deepseek-v4-flash-vision-exp",
	},
	"anthropic": {
		"claude-fable-5", "claude-mythos-5", "claude-opus-5",
		"claude-sonnet-5", "claude-haiku-4-5", "claude-haiku-4-5-20251001",
		"claude-opus-4-8", "claude-opus-4-7", "claude-sonnet-4-6",
		"claude-sonnet-4-5", "claude-opus-4-1",
	},
	"bytedance": {
		"doubao-seed-evolving", "doubao-seed-2-1-pro", "doubao-seed-2-1-turbo",
		"doubao-seed-2-0-pro", "doubao-seed-2-0-lite", "doubao-seed-2-0-mini",
		"doubao-seed-2-0-code", "doubao-seed-1-8", "doubao-seed-1-6-vision",
		"doubao-embedding-large", "doubao-embedding-vision",
		"doubao-seedream-5-0-pro", "doubao-seedream-5-0", "doubao-seedream-4-5",
		"doubao-seedream-4-0", "doubao-seedance-2-5", "doubao-seedance-2-0",
		"doubao-seedance-2-0-fast", "doubao-seedance-2-0-mini",
		"doubao-seedance-1-5-pro", "doubao-seedance-1-0-pro",
		"doubao-seedance-1-0-lite-t2v", "doubao-seedance-1-0-lite-i2v",
	},
	"kimi": {
		"kimi-k3", "kimi-k2.7-code", "kimi-k2.7-code-highspeed", "kimi-k2.6",
		"kimi-k2.5", "moonshot-v1-8k", "moonshot-v1-32k", "moonshot-v1-128k",
		"moonshot-v1-8k-vision-preview", "moonshot-v1-32k-vision-preview",
		"moonshot-v1-128k-vision-preview",
	},
	"minimax": {
		"MiniMax-M3", "MiniMax-M2.7", "MiniMax-M2.7-highspeed",
		"MiniMax-M2.5", "MiniMax-M2.5-highspeed", "MiniMax-M2.1",
		"MiniMax-M2.1-highspeed", "MiniMax-M2", "speech-2.8-hd",
		"speech-2.8-turbo", "speech-2.6-hd", "speech-2.6-turbo",
		"speech-02-hd", "speech-02-turbo", "image-01", "image-01-live",
		"MiniMax-Hailuo-2.3", "MiniMax-Hailuo-2.3-Fast", "MiniMax-Hailuo-02",
		"MiniMax-H3", "MiniMax-H3-Context-IR", "music-3.0", "music-3.0-free",
		"music-2.6", "music-2.6-free",
	},
	"qwen": {
		"qwen3.8-max-preview", "qwen3.7-max", "qwen3.7-plus",
		"qwen3.7-flash", "qwen3-vl-plus", "qwen3-vl-flash", "qwen-plus",
		"qwen-turbo", "qwen-flash", "qwen-max", "text-embedding-v4",
		"qwen3-vl-embedding",
	},
}

var providerCatalogCache struct {
	once     sync.Once
	models   map[string][]ModelTemplate
	defaults map[string]string
	errs     map[string]string
}

// providerCatalogs builds every driver's built-in catalog once. Each
// driver factory is constructed with an empty spec (azure has no
// built-in catalog and is skipped), so the returned templates are the
// driver's own catalog facts. A provider that fails to build is
// isolated: its entry is empty and its error is recorded, but the
// other providers still load.
func providerCatalogs() (map[string][]ModelTemplate, map[string]string, map[string]string) {
	providerCatalogCache.once.Do(func() {
		models := make(map[string][]ModelTemplate, len(Providers))
		defaults := make(map[string]string, len(Providers))
		errs := make(map[string]string, len(Providers))
		for _, prov := range Providers {
			templates, err := buildProviderTemplates(prov.ID)
			if err != nil {
				errs[prov.ID] = err.Error()
				models[prov.ID] = nil
				continue
			}
			models[prov.ID] = templates
			defaults[prov.ID] = firstNonDeprecated(prov.ID, templates)
		}
		providerCatalogCache.models = models
		providerCatalogCache.defaults = defaults
		providerCatalogCache.errs = errs
	})
	return providerCatalogCache.models, providerCatalogCache.defaults, providerCatalogCache.errs
}

// ModelCatalog returns every provider's built-in model catalog for the
// settings page dropdown.
func ModelCatalog() ([]ProviderModels, error) {
	models, _, errs := providerCatalogs()
	out := make([]ProviderModels, 0, len(models))
	for _, prov := range Providers {
		out = append(out, ProviderModels{
			Provider: prov.ID,
			Models:   models[prov.ID],
			Error:    errs[prov.ID],
		})
	}
	return out, nil
}

// init refreshes each provider's DefaultModel from the driver catalog:
// the first non-deprecated model in the driver's declaration order.
func init() {
	_, defaults, errs := providerCatalogs()
	for i := range Providers {
		id := Providers[i].ID
		if errs[id] != "" {
			continue
		}
		if d := defaults[id]; d != "" {
			Providers[i].DefaultModel = d
		}
	}
}

// buildProviderTemplates constructs one driver with an empty spec and
// reads its merged built-in catalog. Azure is special: it has no
// built-in catalog (deployments are user-defined), so it returns no
// templates.
func buildProviderTemplates(providerID string) ([]ModelTemplate, error) {
	if providerID == "azure" {
		return nil, nil
	}
	var factory resource.Factory
	var settings string
	switch providerID {
	case "openai":
		factory, settings = openai.Factory(), `{"id":"openai","spec":{}}`
	case "deepseek":
		factory, settings = deepseek.Factory(), `{"id":"deepseek","spec":{}}`
	case "anthropic":
		factory, settings = anthropic.Factory(), `{"id":"anthropic","spec":{}}`
	case "bytedance":
		factory, settings = bytedance.Factory(), `{"id":"bytedance","spec":{}}`
	case "kimi":
		factory, settings = kimi.Factory(), `{"id":"kimi","spec":{}}`
	case "minimax":
		factory, settings = minimax.Factory(), `{"id":"minimax","spec":{}}`
	case "qwen":
		factory, settings = qwen.Factory(), `{"id":"qwen","spec":{}}`
	default:
		return nil, fmt.Errorf("unknown provider %q", providerID)
	}
	raw, err := factory.New(context.Background(), resource.Input{
		Settings: []byte(settings),
	})
	if err != nil {
		return nil, err
	}
	def, ok := raw.(inference.ProviderDefinition)
	if !ok {
		return nil, fmt.Errorf("unexpected provider value %T", raw)
	}
	return templatesFromDefinition(providerID, def), nil
}

// templatesFromDefinition lowers a built provider definition into
// editor templates. Model kind is derived from the bound openers and
// declared outputs, since descriptors do not carry the driver's
// internal kind directly.
func templatesFromDefinition(
	providerID string,
	def inference.ProviderDefinition,
) []ModelTemplate {
	out := make([]ModelTemplate, 0, len(def.Models))
	overrides := catalogControlOverrides[providerID]
	for _, impl := range def.Models {
		d := impl.Descriptor
		flags := overrides[d.ID.Name]
		replacement := ""
		if d.Lifecycle.Replacement != nil {
			replacement = d.Lifecycle.Replacement.Name
		}
		inputs := PartKindStrings(d.Capabilities.Inputs)
		outputs := PartKindStrings(d.Capabilities.Outputs)
		if inputs == nil {
			inputs = []string{}
		}
		if outputs == nil {
			outputs = []string{}
		}
		out = append(out, ModelTemplate{
			Name:               d.ID.Name,
			Kind:               templateKind(impl.Openers, d.Capabilities),
			Inputs:             inputs,
			Outputs:            outputs,
			Reasoning:          string(d.Capabilities.Reasoning.Kind),
			ReasoningEffortMap: EffortMapStrings(d.Capabilities.Reasoning.EffortMap),
			WebSearch:          d.Capabilities.HostedWebSearch,
			Dimensions:         flags.Dimensions,
			EffortNone:         flags.EffortNone,
			Deprecated:         d.Lifecycle.Status == inference.ModelStatusDeprecated,
			Replacement:        replacement,
			MaxInputTokens:     d.Limits.MaxInputTokens,
		})
	}
	return out
}

// templateKind derives opencraft's Model.Kind vocabulary from the
// bound operation openers and declared outputs.
func templateKind(openers inference.Openers, caps inference.ModelCapabilities) string {
	switch {
	case openers.Embed != nil:
		return "embed"
	case openers.Transcribe != nil:
		// opencraft has no transcribe kind yet; leave it to the
		// writer's output-based derivation.
		return ""
	case slices.Contains(caps.Outputs, message.PartVideo):
		return "video"
	case slices.Contains(caps.Outputs, message.PartImage):
		return "image"
	case slices.Contains(caps.Outputs, message.PartAudio):
		return "tts"
	default:
		return "generate"
	}
}

// firstNonDeprecated returns the first catalog model in the driver's
// declaration order that is not deprecated, or "" when every model is
// deprecated or the provider has no catalog.
func firstNonDeprecated(
	providerID string,
	templates []ModelTemplate,
) string {
	byName := make(map[string]ModelTemplate, len(templates))
	for _, t := range templates {
		byName[t.Name] = t
	}
	order := catalogDefaultOrder[providerID]
	if len(order) == 0 {
		for _, t := range templates {
			order = append(order, t.Name)
		}
	}
	for _, name := range order {
		if t, ok := byName[name]; ok && !t.Deprecated {
			return name
		}
	}
	return ""
}

// EffortMapStrings converts canonical ReasoningEffort keys to their
// wire string form.
func EffortMapStrings(
	efforts map[inference.ReasoningEffort]string,
) map[string]string {
	if len(efforts) == 0 {
		return nil
	}
	out := make(map[string]string, len(efforts))
	for effort, mode := range efforts {
		out[string(effort)] = mode
	}
	return out
}

// EffortMapEfforts converts wire string form effort maps back to
// canonical ReasoningEffort keys.
func EffortMapEfforts(
	raw map[string]string,
) map[inference.ReasoningEffort]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[inference.ReasoningEffort]string, len(raw))
	for effort, mode := range raw {
		out[inference.ReasoningEffort(effort)] = mode
	}
	return out
}
