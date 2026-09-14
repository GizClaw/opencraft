package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/GizClaw/flowcraft/core/inference/model"
	"sigs.k8s.io/yaml"

	"github.com/GizClaw/opencraft/internal/foundation/compat"
)

// Reading the user layer back: the on-disk shapes the writer emits (and
// the ones it used to emit) plus the projection onto the typed config.

// providerSpecDoc is the on-disk provider spec, covering every shape
// the writer emits: the OpenAI wire family and Anthropic use an
// endpoint object, ByteDance takes flat transport fields, and MiniMax
// names its media origin separately.
type providerSpecDoc struct {
	API      string `json:"api"`
	BaseURL  string `json:"base_url"`
	Endpoint struct {
		BaseURL      string            `json:"base_url"`
		Routing      string            `json:"routing"`
		Query        map[string]string `json:"query"`
		Headers      map[string]string `json:"headers"`
		Organization string            `json:"organization"`
		Project      string            `json:"project"`
		Timeout      string            `json:"timeout"`
	} `json:"endpoint"`
	Auth struct {
		Scheme string `json:"scheme"`
		Header string `json:"header"`
	} `json:"auth"`
	Wire struct {
		Store                   json.RawMessage            `json:"store"`
		IncludeReasoningPayload *bool                      `json:"include_reasoning_payload"`
		ReasoningChannel        string                     `json:"reasoning_channel"`
		ReasoningSummary        string                     `json:"reasoning_summary"`
		ReasoningScope          string                     `json:"reasoning_scope"`
		Truncation              string                     `json:"truncation"`
		VideoInput              bool                       `json:"video_input"`
		ExtraBody               map[string]json.RawMessage `json:"extra_body"`
		ChatStreamOptions       struct {
			IncludeUsage       *bool `json:"include_usage"`
			IncludeObfuscation *bool `json:"include_obfuscation"`
		} `json:"chat_stream_options"`
	} `json:"wire"`
	// Legacy fields an older build wrote at this level; the compat
	// package owns their shape.
	compat.LegacyProviderSpec
	HTTPRetries    *int              `json:"http_retries"`
	Region         string            `json:"region"`
	Project        string            `json:"project"`
	Timeout        string            `json:"timeout"`
	ReasoningScope string            `json:"reasoning_scope"`
	Headers        map[string]string `json:"headers"`
	Query          map[string]string `json:"query"`
	MediaBaseURL   string            `json:"media_base_url"`
	VideoPollMS    *int              `json:"video_poll_interval_millis"`
	// RequestMeta is a pointer so an explicit opt-out (`{}`) is
	// distinguishable from an absent block: absent keeps the driver
	// default, present-but-empty disables forwarding.
	RequestMeta *struct {
		Envelope string `json:"envelope"`
	} `json:"request_metadata"`
	Models []providerModelDoc `json:"models"`
}

// providerModelDoc is one declared model in a provider spec.
type providerModelDoc struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Capabilities struct {
		Inputs          []string                  `json:"inputs"`
		Outputs         []string                  `json:"outputs"`
		Reasoning       model.ReasoningCapability `json:"reasoning"`
		HostedWebSearch bool                      `json:"hosted_web_search"`
	} `json:"capabilities"`
	Limits model.ModelLimits `json:"limits"`
	// Lifecycle is the model's discovery metadata; empty means active.
	Lifecycle model.ModelLifecycle `json:"lifecycle"`
}

// advancedFromSpec projects one parsed provider spec onto the typed
// storePolicy reads the retention policy back out of one wire document.
// The wire accepts a JSON boolean or the string "omit"; the settings page
// edits the three values as one string.
func storePolicy(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err == nil {
		if value {
			return "true"
		}
		return "false"
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil && mode == "omit" {
		return "omit"
	}
	return ""
}

// rawMapStrings renders one raw-JSON map as editable text, one entry per
// key, so the settings page can round-trip values it does not model.
func rawMapStrings(raw map[string]json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		out[key] = string(value)
	}
	return out
}

// modelDriverFields lifts the model-entry keys opencraft does not model
// back out of one provider resource, keyed by model name. Host-managed
// keys are skipped so a hand-edited document cannot smuggle them into
// the bag (the writer emits them from the typed fields).
func modelDriverFields(
	raw json.RawMessage, impl string,
) (map[string]map[string]any, error) {
	var doc struct {
		Settings struct {
			Spec struct {
				Models []map[string]any `json:"models"`
			} `json:"spec"`
		} `json:"settings"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := make(map[string]map[string]any, len(doc.Settings.Spec.Models))
	for _, entry := range doc.Settings.Spec.Models {
		name, _ := entry["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		fields := map[string]any{}
		for key, value := range entry {
			if modelDriverFieldKeys[key] ||
				!allowedModelDriverField(impl, key) {
				continue
			}
			fields[key] = value
		}
		if len(fields) > 0 {
			out[name] = fields
		}
	}
	return out, nil
}

// lifecycleFromDoc projects a parsed model lifecycle onto the
// settings-page view.
func lifecycleFromDoc(
	doc model.ModelLifecycle,
) (ModelLifecycle, error) {
	if doc.Status == "" && doc.Replacement == nil && doc.Notes == "" {
		return ModelLifecycle{}, nil
	}
	out := ModelLifecycle{
		Status: string(doc.Status),
		Notes:  doc.Notes,
	}
	if doc.Replacement != nil {
		out.ReplacementProvider = doc.Replacement.Provider
		out.ReplacementName = doc.Replacement.Name
	}
	return out, nil
}

// advancedFromSpec projects one parsed provider spec onto the typed
// advanced view.
func advancedFromSpec(spec providerSpecDoc) InstanceAdvanced {
	adv := InstanceAdvanced{
		Routing:                 spec.Endpoint.Routing,
		Query:                   spec.Endpoint.Query,
		Headers:                 spec.Endpoint.Headers,
		Organization:            spec.Endpoint.Organization,
		Project:                 spec.Endpoint.Project,
		Timeout:                 spec.Endpoint.Timeout,
		AuthScheme:              spec.Auth.Scheme,
		AuthHeader:              spec.Auth.Header,
		HTTPRetries:             spec.HTTPRetries,
		Store:                   storePolicy(spec.Wire.Store),
		ExtraBody:               rawMapStrings(spec.Wire.ExtraBody),
		IncludeReasoningPayload: spec.Wire.IncludeReasoningPayload,
		ReasoningChannel:        spec.Wire.ReasoningChannel,
		ReasoningSummary:        spec.Wire.ReasoningSummary,
		ReasoningScope:          spec.Wire.ReasoningScope,
		Truncation:              spec.Wire.Truncation,
		ChatIncludeUsage:        spec.Wire.ChatStreamOptions.IncludeUsage,
		ChatIncludeObfuscation:  spec.Wire.ChatStreamOptions.IncludeObfuscation,
		VideoInput:              spec.Wire.VideoInput,
		MediaBaseURL:            spec.MediaBaseURL,
		VideoPollIntervalMillis: spec.VideoPollMS,
	}
	// The chat stream options used to sit at the top level of the spec;
	// a document written then still feeds the typed knobs.
	if legacy := spec.ChatStreamOptions; legacy != nil {
		if adv.ChatIncludeUsage == nil && legacy.IncludeUsage != nil {
			adv.ChatIncludeUsage = legacy.IncludeUsage
		}
		if adv.ChatIncludeObfuscation == nil && legacy.IncludeObfuscation != nil {
			adv.ChatIncludeObfuscation = legacy.IncludeObfuscation
		}
	}
	// ByteDance takes these flat instead of under endpoint.
	if spec.Region != "" || spec.Project != "" || spec.Timeout != "" ||
		len(spec.Headers) > 0 || len(spec.Query) > 0 ||
		spec.ReasoningScope != "" {
		adv.Region = spec.Region
		adv.Project = spec.Project
		adv.Timeout = spec.Timeout
		if adv.ReasoningScope == "" {
			adv.ReasoningScope = spec.ReasoningScope
		}
		adv.Headers = spec.Headers
		adv.Query = spec.Query
	}
	switch {
	case spec.RequestMeta == nil:
		// Absent: provider default.
	case spec.RequestMeta.Envelope == "":
		adv.MetadataEnvelope = "-"
	default:
		if spec.RequestMeta.Envelope != "client_metadata" {
			adv.MetadataEnvelope = spec.RequestMeta.Envelope
		}
	}
	return adv
}

// LoadInference reads the user configuration layer back into an
// InferenceConfig so the settings page can prefill provider/model/key
// edits instead of starting blank. It only understands the sections
// the settings page writes (provider profiles, the Azure provider, and
// the router targets); unknown resources are ignored.
func LoadInference(configDir string) (InferenceConfig, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		// First launch: no user layer yet means no configured
		// instances, not a startup failure. The UI drives the
		// "inference not configured" guide from InferenceNeeded.
		if errors.Is(err, os.ErrNotExist) {
			return InferenceConfig{}, nil
		}
		return InferenceConfig{}, err
	}
	var doc struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return InferenceConfig{}, fmt.Errorf("config: parse user config: %w", err)
	}

	// Provider declarations: one resource per configured instance,
	// with credential profiles and the deployment spec (endpoint /
	// base_url, api mode, models + capabilities).
	type instanceSettings struct {
		Impl     string `json:"impl"`
		Settings struct {
			ID       string `json:"id"`
			Profiles []struct {
				ID        string            `json:"id"`
				Endpoints map[string]string `json:"endpoints"`
				Secrets   struct {
					APIKey string `json:"api_key"`
				} `json:"secrets"`
			} `json:"profiles"`
			Spec providerSpecDoc `json:"spec"`
		} `json:"settings"`
	}
	type parsedProvider struct {
		res instanceSettings
		raw json.RawMessage
	}
	providers := make(map[string]parsedProvider, len(doc.Resources))
	for id, raw := range doc.Resources {
		if !strings.HasPrefix(id, "provider.") {
			continue
		}
		var res instanceSettings
		if err := yaml.Unmarshal(raw, &res); err != nil {
			return InferenceConfig{}, fmt.Errorf("config: parse %s: %w", id, err)
		}
		providers[id] = parsedProvider{res: res, raw: raw}
	}

	// Router targets define provider priority order and model names.
	var router struct {
		Settings struct {
			Retry struct {
				Generate struct {
					MaxAttempts              int  `json:"max_attempts"`
					FallbackOnRetryExhausted bool `json:"fallback_on_retry_exhausted"`
				} `json:"generate"`
			} `json:"retry"`
			Generate []struct {
				Targets []struct {
					Model struct {
						ID struct {
							Provider string `json:"provider"`
							Name     string `json:"name"`
						} `json:"id"`
					} `json:"model"`
				} `json:"targets"`
			} `json:"generate"`
		} `json:"settings"`
	}
	if raw, ok := doc.Resources["router"]; ok {
		if err := yaml.Unmarshal(raw, &router); err != nil {
			return InferenceConfig{}, fmt.Errorf("config: parse router: %w", err)
		}
	}

	cfg := InferenceConfig{
		Router: RouterPolicy{
			MaxAttempts:              router.Settings.Retry.Generate.MaxAttempts,
			FallbackOnRetryExhausted: router.Settings.Retry.Generate.FallbackOnRetryExhausted,
		},
	}
	type routerTarget struct {
		provider string
		model    string
	}
	var targets []routerTarget
	for _, pool := range router.Settings.Generate {
		for _, target := range pool.Targets {
			targets = append(targets, routerTarget{
				provider: target.Model.ID.Provider,
				model:    target.Model.ID.Name,
			})
		}
	}

	// Recover every declared instance (enabled and disabled) from the
	// provider resources in deterministic order, then mark and order
	// the enabled ones by the router targets.
	type parsed struct {
		id string
		in Instance
	}
	providerKeys := make([]string, 0, len(providers))
	for key := range providers {
		providerKeys = append(providerKeys, key)
	}
	sort.Strings(providerKeys)
	var all []parsed
	for _, key := range providerKeys {
		entry := providers[key]
		res := entry.res
		instID := res.Settings.ID
		if instID == "" {
			instID = strings.TrimPrefix(key, "provider.")
		}
		// The driver impl no longer identifies the provider: the whole
		// OpenAI wire family shares one impl, so the deployment id is
		// the authority. An id with no catalog prefix belongs to a
		// plugin-declared provider: its type is whatever names the
		// deployment, and the driver is explicit.
		profileID := ""
		if len(res.Settings.Profiles) > 0 {
			profileID = res.Settings.Profiles[0].ID
		}
		instType := instanceTypeFromID(instID)
		explicitDriver := ""
		if instType == "" {
			instType = strings.TrimSuffix(instID, "-"+profileID)
			if instType == "" {
				instType = instID
			}
			explicitDriver = res.Impl
		}
		in := Instance{Type: instType}
		if explicitDriver != "" {
			in.Driver = explicitDriver
		}
		// The stable identity lives in the profile id.
		if len(res.Settings.Profiles) > 0 {
			if pid := res.Settings.Profiles[0].ID; pid != "" {
				in.StableID = pid
			}
			k := res.Settings.Profiles[0].Secrets.APIKey
			if strings.HasPrefix(k, "${env:") && strings.HasSuffix(k, "}") {
				in.KeySource = KeyEnv
			} else if strings.HasPrefix(k, "${secret:keychain.") && strings.HasSuffix(k, "}") {
				in.KeySource = KeyKeychain
				in.KeyValue = strings.TrimSuffix(
					strings.TrimPrefix(k, "${secret:keychain."), "}")
			} else {
				in.KeySource = KeyLiteral
				in.KeyValue = k
			}
		}
		spec := res.Settings.Spec
		in.API = spec.API
		in.Endpoint = spec.BaseURL
		if spec.Endpoint.BaseURL != "" {
			in.Endpoint = spec.Endpoint.BaseURL
		}
		if spec.MediaBaseURL != "" {
			in.Endpoint = spec.MediaBaseURL
		}
		in.Advanced = advancedFromSpec(spec)
		driverFields, err := modelDriverFields(entry.raw, res.Impl)
		if err != nil {
			return InferenceConfig{}, fmt.Errorf(
				"config: %s model fields: %w", instID, err)
		}
		var endpoints map[string]string
		if len(res.Settings.Profiles) > 0 {
			endpoints = res.Settings.Profiles[0].Endpoints
		}
		for _, declared := range spec.Models {
			lifecycle, err := lifecycleFromDoc(declared.Lifecycle)
			if err != nil {
				return InferenceConfig{}, fmt.Errorf(
					"config: %s model %s: %w", instID, declared.Name, err,
				)
			}
			m := Model{
				Name: declared.Name,
				Kind: declared.Kind,
				Capabilities: model.ModelCapabilities{
					Inputs:          ToPartKinds(declared.Capabilities.Inputs),
					Outputs:         ToPartKinds(declared.Capabilities.Outputs),
					Reasoning:       declared.Capabilities.Reasoning,
					HostedWebSearch: declared.Capabilities.HostedWebSearch,
				},
				Limits:       declared.Limits,
				Lifecycle:    lifecycle,
				DriverFields: driverFields[declared.Name],
			}
			if endpoint := endpoints[m.Name]; endpoint != "" {
				m.Endpoint = endpoint
			}
			in.Models = append(in.Models, m)
		}
		all = append(all, parsed{id: instID, in: in})
	}

	consumed := make(map[string]bool)
	for _, t := range targets {
		for i := range all {
			if all[i].id == t.provider && !consumed[t.provider] {
				all[i].in.Enabled = true
				if t.model != "" {
					// The router names the served models; spec models
					// carry the capabilities. Merge so a hand-written
					// target that is not declared in the spec is still
					// round-tripped back to the settings page.
					all[i].in.addModel(t.model)
				}
				consumed[t.provider] = true
				cfg.Instances = append(cfg.Instances, all[i].in)
				break
			}
		}
	}
	for _, p := range all {
		if !consumed[p.id] {
			cfg.Instances = append(cfg.Instances, p.in)
		}
	}
	return cfg, nil
}

// addModel appends a model name to the instance unless it is already
// declared, preserving the declared order.
func (in *Instance) addModel(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for _, m := range in.Models {
		if strings.TrimSpace(m.Name) == name {
			return
		}
	}
	in.Models = append(in.Models, Model{Name: name})
}

// instanceTypeFromID maps a provider deployment id back to its catalog
// type: "<type>-<n>" or "<type>-<stableID>".
func instanceTypeFromID(id string) string {
	best := ""
	for _, p := range Providers {
		if strings.HasPrefix(id, p.ID+"-") && len(p.ID) > len(best) {
			best = p.ID
		}
	}
	return best
}
