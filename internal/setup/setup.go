// Package setup owns opencraft's first-run inference configuration:
// detecting whether inference wiring exists, generating the user
// configuration layer (~/.opencraft/config/opencraft.yaml) from wizard
// choices, and writing it to the user configuration directory. That
// file is opencraft's single user-editable document going forward.
//
// Design: every provider is registered into the infer assembly at setup
// time (Azure only when the user configures it, since it needs an
// endpoint and deployment). The user only supplies keys for the
// providers they have; the router policy lists the keyed providers in
// priority order with retry fallback, so routing is automatic.
// The interactive wizard lives in ui.go; generation and detection are
// pure functions so they are testable without a terminal.
package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// Provider is one supported inference provider in the wizard.
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

// Providers is the wizard's provider catalog, ordered by recommendation.
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

// Config is one completed wizard configuration: the subset of
// providers the user has keys for.
type Config struct {
	Providers []KeyedProvider
}

// keyed returns the entry for a provider id, if selected.
func (c Config) keyed(id string) (KeyedProvider, bool) {
	for _, k := range c.Providers {
		if k.Provider.ID == id {
			return k, true
		}
	}
	return KeyedProvider{}, false
}

// Needed reports whether the user configuration directory lacks
// inference wiring, i.e. the user opencraft.yaml layer does not declare
// an infer assembly.
func Needed(configDir string) (bool, error) {
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
		// Unparseable user layer: treat as unconfigured so the wizard
		// can rewrite the user layer without touching the file.
		return true, nil
	}
	// The embedded inference layer provides providers + infer + the
	// router retry shell; the setup-written user layer is what adds the
	// router's generate targets (and key profiles), so its presence
	// marks a configured install.
	if _, ok := doc.Resources["router"]; ok {
		return false, nil
	}
	return true, nil
}

// UserConfigYAML renders the generated user configuration layer
// (~/.opencraft/config/opencraft.yaml). The FIXED inference wiring —
// every provider resource, the infer assembly, and the router retry
// shell — is embedded in the binary (assets/inference.yaml). This
// document only carries the VARIABLE parts: credential profiles for
// the keyed providers, an optional Azure provider (endpoint +
// deployment are per-user), and the router's generate targets (keyed
// providers in priority order; the router falls back on failure).
func (c Config) UserConfigYAML() ([]byte, error) {
	if len(c.Providers) == 0 {
		return nil, errors.New("setup: at least one provider is required")
	}
	for _, k := range c.Providers {
		if k.Provider.Azure {
			if strings.TrimSpace(k.Endpoint) == "" {
				return nil, fmt.Errorf("setup: azure endpoint is required")
			}
			if strings.TrimSpace(k.Model) == "" {
				return nil, fmt.Errorf("setup: azure deployment is required")
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# opencraft user configuration layer\n")
	fmt.Fprintf(&b, "# (~/.opencraft/config/opencraft.yaml; generated by first-run setup\n")
	fmt.Fprintf(&b, "# at %s). Re-run `opencraft setup` to change\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "# provider / model / key.\n")
	fmt.Fprintf(&b, "#\n")
	fmt.Fprintf(&b, "# The FIXED inference wiring lives in the binary (embedded\n")
	fmt.Fprintf(&b, "# inference.yaml): all provider resources, the infer assembly,\n")
	fmt.Fprintf(&b, "# and the router retry policy. This file carries only the\n")
	fmt.Fprintf(&b, "# VARIABLE parts: API key profiles, Azure (when configured),\n")
	fmt.Fprintf(&b, "# and the router's generate targets (keyed providers in\n")
	fmt.Fprintf(&b, "# priority order; failures fall back to the next target).\n")
	fmt.Fprintf(&b, "# Resources, deps, and settings merge deeply across layers.\n")
	fmt.Fprintf(&b, "#\n")
	fmt.Fprintf(&b, "# To add a key later, add a profiles block under that provider\n")
	fmt.Fprintf(&b, "# and a target under router.settings.generate, or re-run\n")
	fmt.Fprintf(&b, "# `opencraft setup`. To change model priority, edit\n")
	fmt.Fprintf(&b, "# router.settings.generate[].targets order.\n")
	fmt.Fprintf(&b, "#\n")
	fmt.Fprintf(&b, "# Examples (uncomment to use):\n")
	fmt.Fprintf(&b, "# - Attach an external MCP server's tools (lazy-discovered via\n")
	fmt.Fprintf(&b, "#   tool_search):\n")
	fmt.Fprintf(&b, "#   resources:\n")
	fmt.Fprintf(&b, "#     tool.mcp:\n")
	fmt.Fprintf(&b, "#       kind: tool.Source\n")
	fmt.Fprintf(&b, "#       impl: mcp\n")
	fmt.Fprintf(&b, "#       settings:\n")
	fmt.Fprintf(&b, "#         servers:\n")
	fmt.Fprintf(&b, "#           - name: my-server\n")
	fmt.Fprintf(&b, "#             transport: stdio\n")
	fmt.Fprintf(&b, "#             command: my-mcp-server\n")
	fmt.Fprintf(&b, "#     tools:\n")
	fmt.Fprintf(&b, "#       deps:\n")
	fmt.Fprintf(&b, "#         tool.mcp: tool.mcp\n")
	fmt.Fprintf(&b, "# - Restrict the sandbox environment:\n")
	fmt.Fprintf(&b, "#   resources:\n")
	fmt.Fprintf(&b, "#     box:\n")
	fmt.Fprintf(&b, "#       settings:\n")
	fmt.Fprintf(&b, "#         env_policy:\n")
	fmt.Fprintf(&b, "#           allow: [\"PATH\", \"HOME\", \"GOPROXY\"]\n")
	fmt.Fprintf(&b, "#           inject:\n")
	fmt.Fprintf(&b, "#             GOMODCACHE: ${env:OPEN_CRAFT_CACHE}/go/pkg/mod\n")
	fmt.Fprintf(&b, "# - Use your own graph (file refs resolve under this directory):\n")
	fmt.Fprintf(&b, "#   agents:\n")
	fmt.Fprintf(&b, "#     assistant:\n")
	fmt.Fprintf(&b, "#       engine:\n")
	fmt.Fprintf(&b, "#         settings:\n")
	fmt.Fprintf(&b, "#           graph: {file: graphs/my-assistant.yaml}\n")
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
		// deployments to declare text output; setup emits the minimal
		// mandatory declaration. Vision / reasoning / hosted web
		// search can be added manually via capabilities until the
		// wizard grows those knobs.
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

// Write persists the wizard output into the user configuration
// directory: opencraft.yaml (the single editable document).
func (c Config) Write(configDir string) error {
	userDoc, err := c.UserConfigYAML()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("setup: create config dir: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, "opencraft.yaml"), userDoc, 0o600,
	); err != nil {
		return fmt.Errorf("setup: write opencraft.yaml: %w", err)
	}
	return nil
}

// Load reads the user configuration layer back into a Config so the
// UI can prefill provider/model/key edits instead of starting blank.
// It only understands the sections setup writes (provider profiles,
// the Azure provider, and the router targets); unknown resources are
// ignored. A later Write regenerates the whole layer.
func Load(configDir string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		return Config{}, err
	}
	var doc struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Config{}, fmt.Errorf("setup: parse user config: %w", err)
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
			return Config{}, fmt.Errorf("setup: parse %s: %w", id, err)
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
			return Config{}, fmt.Errorf("setup: parse router: %w", err)
		}
	}

	cfg := Config{}
	for _, pool := range router.Settings.Generate {
		for _, target := range pool.Targets {
			prov, ok := providerByID(target.Model.ID.Provider)
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

// providerByID resolves the catalog entry for one provider id.
func providerByID(id string) (Provider, bool) {
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// yamlQuote quotes a plain scalar safely (single-quote style).
func yamlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
