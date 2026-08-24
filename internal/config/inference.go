package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	yamlv4 "go.yaml.in/yaml/v4"
	"sigs.k8s.io/yaml"
)

// Inference wiring lives in the user configuration layer
// (~/.opencraft/config/opencraft.yaml), edited through the desktop
// settings page: every provider is registered into the infer assembly
// (Azure only when the user configures it, since it needs an endpoint
// and deployment). The user only supplies keys for the providers they
// have; the router policy lists the keyed providers in priority order
// with retry fallback, so routing is automatic.

// Provider is one supported inference provider.
type Provider struct {
	ID           string // deploy resource id (provider.<id>)
	Impl         string // driver impl registered in the runtime
	Name         string // display name
	DefaultModel string // prefilled model / Azure deployment name
	EnvVar       string // conventional API key environment variable
	// API is the generate surface for OpenAI-compatible drivers
	// ("responses" or "chat"); empty uses the driver default.
	API string
	// Azure routes by deployment name and needs an endpoint; the model
	// input becomes the deployment name and the generated spec declares
	// it explicitly.
	Azure bool
}

// Providers is the provider catalog, ordered by recommendation.
var Providers = []Provider{
	{ID: "deepseek", Impl: "deepseek", Name: "DeepSeek", DefaultModel: "deepseek-v4-flash", EnvVar: "DEEPSEEK_API_KEY", API: "responses"},
	{ID: "openai", Impl: "openai", Name: "OpenAI", DefaultModel: "gpt-5.6-sol", EnvVar: "OPENAI_API_KEY", API: "responses"},
	{ID: "anthropic", Impl: "anthropic", Name: "Anthropic", DefaultModel: "claude-sonnet-5", EnvVar: "ANTHROPIC_API_KEY"},
	{ID: "azure", Impl: "azure", Name: "Azure OpenAI", DefaultModel: "", EnvVar: "AZURE_OPENAI_API_KEY", Azure: true},
	{ID: "bytedance", Impl: "bytedance", Name: "ByteDance (Ark)", DefaultModel: "doubao-seed-2-1-pro", EnvVar: "ARK_API_KEY"},
	{ID: "kimi", Impl: "kimi", Name: "Kimi (Moonshot)", DefaultModel: "kimi-k3", EnvVar: "MOONSHOT_API_KEY"},
	{ID: "minimax", Impl: "minimax", Name: "MiniMax", DefaultModel: "MiniMax-M3", EnvVar: "MINIMAX_API_KEY"},
	{ID: "qwen", Impl: "qwen", Name: "Qwen (DashScope)", DefaultModel: "qwen3.7-max", EnvVar: "DASHSCOPE_API_KEY"},
}

// ProviderByID resolves the catalog entry for one provider id.
func ProviderByID(id string) (Provider, bool) {
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// KeySource selects how the API key is stored.
type KeySource int

const (
	// KeyEnv references the provider's environment variable
	// (${env:VAR}); the secret never touches disk.
	KeyEnv KeySource = iota
	// KeyLiteral stores the key verbatim in opencraft.yaml (0600).
	KeyLiteral
)

// KeyedProvider is one provider the user holds a key for, in priority
// order (router tries them in order and falls back on failure).
type KeyedProvider struct {
	Provider  Provider
	KeySource KeySource
	KeyValue  string // literal key (KeyLiteral only)
	Model     string // Azure: deployment name; empty uses the default
	Endpoint  string // Azure resource URL
	// Azure capability declarations (capability-aware routing, core
	// v0.1.22+). Text output is always emitted for generate
	// deployments; these knobs declare the optional channels.
	Vision    bool   // capabilities.inputs: [image]
	Reasoning string // "" | "always" | "toggle" → capabilities.reasoning
	WebSearch bool   // capabilities.hosted_web_search: true
}

// InferenceConfig is one completed inference configuration: the
// subset of providers the user has keys for, in router priority order.
type InferenceConfig struct {
	Providers []KeyedProvider
}

// keyed returns the entry for a provider id, if selected.
func (c InferenceConfig) keyed(id string) (KeyedProvider, bool) {
	for _, k := range c.Providers {
		if k.Provider.ID == id {
			return k, true
		}
	}
	return KeyedProvider{}, false
}

// InferenceNeeded reports whether the user configuration directory
// lacks inference wiring, i.e. the user opencraft.yaml layer does not
// declare a router.
func InferenceNeeded(configDir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		// No user layer: definitely unconfigured.
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	var doc struct {
		Resources map[string]any `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// Unparseable user layer: treat as unconfigured so the
		// settings page can rewrite it.
		return true, nil
	}
	// The embedded inference layer provides providers + infer + the
	// router retry shell; the user-written layer is what adds the
	// router's generate targets (and key profiles), so its presence
	// marks a configured install.
	if _, ok := doc.Resources["router"]; ok {
		return false, nil
	}
	return true, nil
}

// InferenceYAML renders the user configuration layer
// (~/.opencraft/config/opencraft.yaml). The FIXED inference wiring —
// every provider resource, the infer assembly, and the router retry
// shell — is embedded in the binary (assets/inference.yaml). This
// document only carries the VARIABLE parts: credential profiles for
// the keyed providers, an optional Azure provider (endpoint +
// deployment are per-user), and the router's generate targets (keyed
// providers in priority order; the router falls back on failure).
func (c InferenceConfig) InferenceYAML() ([]byte, error) {
	if len(c.Providers) == 0 {
		return nil, errors.New("config: at least one provider is required")
	}
	for _, k := range c.Providers {
		if k.Provider.Azure {
			if strings.TrimSpace(k.Endpoint) == "" {
				return nil, fmt.Errorf("config: azure endpoint is required")
			}
			if strings.TrimSpace(k.Model) == "" {
				return nil, fmt.Errorf("config: azure deployment is required")
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# opencraft user configuration layer\n")
	fmt.Fprintf(&b, "# (~/.opencraft/config/opencraft.yaml; edited through the\n")
	fmt.Fprintf(&b, "# desktop settings page; last written %s).\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "#\n")
	fmt.Fprintf(&b, "# The FIXED inference wiring lives in the binary (embedded\n")
	fmt.Fprintf(&b, "# inference.yaml): all provider resources, the infer assembly,\n")
	fmt.Fprintf(&b, "# and the router retry policy. This file carries only the\n")
	fmt.Fprintf(&b, "# VARIABLE parts: API key profiles, Azure (when configured),\n")
	fmt.Fprintf(&b, "# and the router's generate targets (keyed providers in\n")
	fmt.Fprintf(&b, "# priority order; failures fall back to the next target).\n")
	fmt.Fprintf(&b, "# Resources, deps, and settings merge deeply across layers, so\n")
	fmt.Fprintf(&b, "# resources not managed here (MCP servers, sandbox policy,\n")
	fmt.Fprintf(&b, "# custom graphs) are preserved across settings writes.\n")
	fmt.Fprintf(&b, "version: v1\n")
	fmt.Fprintf(&b, "resources:\n")
	for _, k := range c.Providers {
		if k.Provider.Azure {
			continue
		}
		// Partial provider declaration: the embedded layer already
		// defines kind/impl/id/spec; only the credential profile is
		// added here.
		fmt.Fprintf(&b, "  provider.%s:\n", k.Provider.ID)
		fmt.Fprintf(&b, "    settings:\n")
		fmt.Fprintf(&b, "      profiles:\n")
		// No profile id: the router policy references the implicit
		// empty profile id, matching the provider driver defaults.
		fmt.Fprintf(&b, "        - secrets:\n")
		fmt.Fprintf(&b, "            api_key: %s\n", k.apiKey())
	}
	if azure, ok := c.keyed("azure"); ok {
		// Azure cannot be embedded: its spec requires an endpoint and
		// a deployment, both per-user. Declare the full resource and
		// attach it to the (embedded) infer assembly via dep merge.
		fmt.Fprintf(&b, "  provider.azure:\n")
		fmt.Fprintf(&b, "    kind: inference.Provider\n")
		fmt.Fprintf(&b, "    impl: azure\n")
		fmt.Fprintf(&b, "    settings:\n")
		fmt.Fprintf(&b, "      id: azure\n")
		fmt.Fprintf(&b, "      spec:\n")
		fmt.Fprintf(&b, "        endpoint: %s\n", yamlQuote(azure.Endpoint))
		fmt.Fprintf(&b, "        models:\n")
		fmt.Fprintf(&b, "          - name: %s\n", yamlQuote(azure.Model))
		fmt.Fprintf(&b, "            kind: generate\n")
		// Capability-aware routing (core v0.1.22+) requires generate
		// deployments to declare text output; the minimal mandatory
		// declaration is emitted. Vision / reasoning / hosted web
		// search are declared when configured.
		fmt.Fprintf(&b, "            capabilities:\n")
		fmt.Fprintf(&b, "              outputs: [text]\n")
		if azure.Vision {
			fmt.Fprintf(&b, "              inputs: [image]\n")
		}
		if azure.Reasoning != "" {
			fmt.Fprintf(&b, "              reasoning: %s\n", yamlQuote(azure.Reasoning))
		}
		if azure.WebSearch {
			fmt.Fprintf(&b, "              hosted_web_search: true\n")
		}
		fmt.Fprintf(&b, "      profiles:\n")
		fmt.Fprintf(&b, "        - secrets:\n")
		fmt.Fprintf(&b, "            api_key: %s\n", azure.apiKey())
		fmt.Fprintf(&b, "  infer:\n")
		fmt.Fprintf(&b, "    deps:\n")
		fmt.Fprintf(&b, "      provider.azure: provider.azure\n")
	}
	fmt.Fprintf(&b, "  router:\n")
	fmt.Fprintf(&b, "    settings:\n")
	fmt.Fprintf(&b, "      generate:\n")
	fmt.Fprintf(&b, "        - tier: default\n")
	fmt.Fprintf(&b, "          targets:\n")
	for _, k := range c.Providers {
		model := k.Model
		if model == "" {
			model = k.Provider.DefaultModel
		}
		fmt.Fprintf(&b, "            - model:\n")
		fmt.Fprintf(&b, "                id:\n")
		fmt.Fprintf(&b, "                  provider: %s\n", k.Provider.ID)
		fmt.Fprintf(&b, "                  name: %s\n", yamlQuote(model))
	}
	return []byte(b.String()), nil
}

// apiKey renders the profile secret value.
func (k KeyedProvider) apiKey() string {
	if k.KeySource == KeyEnv {
		return "${env:" + k.Provider.EnvVar + "}"
	}
	return yamlQuote(k.KeyValue)
}

// WriteInference persists the inference configuration into the user
// configuration directory (opencraft.yaml), merging over the existing
// layer so resources the settings page does not manage (MCP servers,
// sandbox policy, custom graphs) are preserved.
func WriteInference(configDir string, cfg InferenceConfig) error {
	fresh, err := cfg.InferenceYAML()
	if err != nil {
		return err
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		managedResourceKeys(cfg),
		map[string]bool{},
	)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("config: create config dir: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, "opencraft.yaml"), merged, 0o600,
	); err != nil {
		return fmt.Errorf("config: write opencraft.yaml: %w", err)
	}
	return nil
}

// managedResourceKeys returns the resources WriteInference owns: every
// provider declaration, the router, and the infer dep wiring. Infer is
// managed even when Azure is not selected so a stale dep left by a
// previous Azure configuration is removed instead of referencing a
// deleted provider. Everything else in the user layer is preserved.
func managedResourceKeys(cfg InferenceConfig) map[string]bool {
	keys := map[string]bool{"router": true, "infer": true}
	for _, k := range cfg.Providers {
		keys["provider."+k.Provider.ID] = true
	}
	return keys
}

// LoadInference reads the user configuration layer back into an
// InferenceConfig so the settings page can prefill provider/model/key
// edits instead of starting blank. It only understands the sections
// the settings page writes (provider profiles, the Azure provider, and
// the router targets); unknown resources are ignored.
func LoadInference(configDir string) (InferenceConfig, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		return InferenceConfig{}, err
	}
	var doc struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return InferenceConfig{}, fmt.Errorf("config: parse user config: %w", err)
	}

	// Provider declarations: credential profiles (and the Azure
	// endpoint/deployment/capabilities) live under provider.<id>.
	type providerSettings struct {
		Settings struct {
			Profiles []struct {
				Secrets struct {
					APIKey string `json:"api_key"`
				} `json:"secrets"`
			} `json:"profiles"`
			Spec struct {
				Endpoint string `json:"endpoint"`
				Models   []struct {
					Name         string `json:"name"`
					Capabilities struct {
						Inputs          []string `json:"inputs"`
						Reasoning       string   `json:"reasoning"`
						HostedWebSearch bool     `json:"hosted_web_search"`
					} `json:"capabilities"`
				} `json:"models"`
			} `json:"spec"`
		} `json:"settings"`
	}
	providers := make(map[string]providerSettings, len(doc.Resources))
	for id, raw := range doc.Resources {
		if !strings.HasPrefix(id, "provider.") {
			continue
		}
		var res providerSettings
		if err := yaml.Unmarshal(raw, &res); err != nil {
			return InferenceConfig{}, fmt.Errorf("config: parse %s: %w", id, err)
		}
		providers[id] = res
	}

	// Router targets define provider priority order and model names.
	var router struct {
		Settings struct {
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

	cfg := InferenceConfig{}
	for _, pool := range router.Settings.Generate {
		for _, target := range pool.Targets {
			prov, ok := ProviderByID(target.Model.ID.Provider)
			if !ok {
				continue
			}
			keyed := KeyedProvider{
				Provider: prov,
				Model:    target.Model.ID.Name,
			}
			if res, ok := providers["provider."+prov.ID]; ok {
				if len(res.Settings.Profiles) > 0 {
					key := res.Settings.Profiles[0].Secrets.APIKey
					if strings.HasPrefix(key, "${env:") && strings.HasSuffix(key, "}") {
						keyed.KeySource = KeyEnv
					} else {
						keyed.KeySource = KeyLiteral
						keyed.KeyValue = key
					}
				}
				if prov.Azure {
					keyed.Endpoint = res.Settings.Spec.Endpoint
					if len(res.Settings.Spec.Models) > 0 {
						model := res.Settings.Spec.Models[0]
						if keyed.Model == "" {
							keyed.Model = model.Name
						}
						caps := model.Capabilities
						for _, input := range caps.Inputs {
							if input == "image" {
								keyed.Vision = true
							}
						}
						keyed.Reasoning = caps.Reasoning
						keyed.WebSearch = caps.HostedWebSearch
					}
				}
			}
			cfg.Providers = append(cfg.Providers, keyed)
		}
	}
	return cfg, nil
}

// mergeUserLayer merges a freshly generated user document over the
// existing user layer, preserving top-level sections and resources the
// generator does not own. replaceKeys are resources taken verbatim
// from the fresh document; mergeKeys are resources deep-merged (the
// fresh document contributes only the keys it sets). Comments are
// preserved through yaml.Node.
func mergeUserLayer(
	path string,
	fresh []byte,
	replaceKeys map[string]bool,
	mergeKeys map[string]bool,
) ([]byte, error) {
	oldData, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fresh, nil
		}
		return nil, fmt.Errorf("config: read user layer: %w", err)
	}
	// Strict parse first: the comment-preserving Node parser is too
	// lenient to detect a broken user layer, and overwriting one would
	// silently destroy the user's hand-written config.
	var probe map[string]any
	if err := yaml.Unmarshal(oldData, &probe); err != nil {
		return nil, fmt.Errorf("config: parse existing user layer: %w", err)
	}
	var oldNode, newRoot yamlv4.Node
	if err := yamlv4.Unmarshal(oldData, &oldNode); err != nil {
		return nil, fmt.Errorf("config: parse existing user layer: %w", err)
	}
	if err := yamlv4.Unmarshal(fresh, &newRoot); err != nil {
		return nil, fmt.Errorf("config: parse generated user layer: %w", err)
	}
	if len(oldNode.Content) == 0 || oldNode.Content[0].Kind != yamlv4.MappingNode {
		return nil, fmt.Errorf(
			"config: existing user layer is not a YAML mapping (kind %d, %d entries); refusing to overwrite it",
			func() uint32 {
				if len(oldNode.Content) > 0 {
					return uint32(oldNode.Content[0].Kind)
				}
				return uint32(0)
			}(),
			len(oldNode.Content),
		)
	}
	oldDoc := oldNode.Content[0]
	newDoc := newRoot.Content[0]

	// Preserve top-level sections the generator does not write (e.g. a
	// custom agents section).
	for i := 0; i+1 < len(oldDoc.Content); i += 2 {
		key := oldDoc.Content[i].Value
		if key == "resources" {
			continue
		}
		if findMappingKey(newDoc, key) == nil {
			newDoc.Content = append(newDoc.Content, oldDoc.Content[i], oldDoc.Content[i+1])
		}
	}

	// Merge resources: generator-owned keys are replaced (or
	// deep-merged), everything else is preserved.
	oldRes := findMappingKey(oldDoc, "resources")
	newRes := findMappingKey(newDoc, "resources")
	if oldRes != nil && newRes != nil && len(oldRes.Content) > 0 {
		for i := 0; i+1 < len(oldRes.Content); i += 2 {
			key := oldRes.Content[i].Value
			if replaceKeys[key] || strings.HasPrefix(key, "provider.") {
				continue
			}
			if mergeKeys[key] {
				if freshRes := findMappingKey(newRes, key); freshRes != nil &&
					freshRes.Content[0] != nil {
					mergeMapping(oldRes.Content[i+1], freshRes.Content[0])
					continue
				}
			}
			if findMappingKey(newRes, key) == nil {
				newRes.Content = append(newRes.Content, oldRes.Content[i], oldRes.Content[i+1])
			}
		}
	}

	var buf bytes.Buffer
	enc := yamlv4.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&newRoot); err != nil {
		return nil, fmt.Errorf("config: encode merged user layer: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// findMappingKey returns the value node for key in a mapping node, or
// nil when absent.
func findMappingKey(mapping *yamlv4.Node, key string) *yamlv4.Node {
	if mapping == nil {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// mergeMapping deep-merges src into dst in place: mapping pairs merge
// recursively, every other value replaces. The src nodes are appended
// as-is so their comments survive.
func mergeMapping(dst, src *yamlv4.Node) {
	if dst == nil || src == nil || dst.Kind != yamlv4.MappingNode || src.Kind != yamlv4.MappingNode {
		return
	}
	for i := 0; i+1 < len(src.Content); i += 2 {
		srcKey := src.Content[i].Value
		srcVal := src.Content[i+1]
		if dstVal := findMappingKey(dst, srcKey); dstVal != nil {
			if dstVal.Kind == yamlv4.MappingNode && srcVal.Kind == yamlv4.MappingNode {
				mergeMapping(dstVal, srcVal)
				continue
			}
			// Replace the existing value pair in place.
			for j := 0; j+1 < len(dst.Content); j += 2 {
				if dst.Content[j].Value == srcKey {
					dst.Content[j+1] = srcVal
					break
				}
			}
			continue
		}
		dst.Content = append(dst.Content, src.Content[i], srcVal)
	}
}

// yamlQuote quotes a plain scalar safely (single-quote style).
func yamlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
