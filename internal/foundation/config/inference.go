package config

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"
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
	// ModelEndpoint marks providers whose deployment binds models to
	// per-model endpoints (ByteDance Ark ep-xxx ids are account-scoped
	// and live in the profile), so the settings UI surfaces the field.
	ModelEndpoint bool
}

// Providers is the provider catalog, ordered by recommendation.
var Providers = []Provider{
	{ID: "deepseek", Impl: "deepseek", Name: "DeepSeek", DefaultModel: "deepseek-v4-flash", EnvVar: "DEEPSEEK_API_KEY", API: "responses"},
	{ID: "openai", Impl: "openai", Name: "OpenAI", DefaultModel: "gpt-5.6-sol", EnvVar: "OPENAI_API_KEY", API: "responses"},
	{ID: "anthropic", Impl: "anthropic", Name: "Anthropic", DefaultModel: "claude-sonnet-5", EnvVar: "ANTHROPIC_API_KEY"},
	{ID: "azure", Impl: "azure", Name: "Azure OpenAI", DefaultModel: "", EnvVar: "AZURE_OPENAI_API_KEY", Azure: true},
	{ID: "bytedance", Impl: "bytedance", Name: "ByteDance (Ark)", DefaultModel: "doubao-seed-2-1-pro", EnvVar: "ARK_API_KEY", ModelEndpoint: true},
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
	// KeyKeychain stores the key in the OS credential store (0600
	// files) and keeps only a ${secret:keychain.<name>} reference in
	// opencraft.yaml.
	KeyKeychain
)

// Model is one model served by an inference instance. Capabilities
// mirrors flowcraft's inference.ModelCapabilities verbatim so no
// capability is lost across the config boundary. A single instance may
// expose several models (e.g. two DeepSeek models sharing one
// endpoint/key); capabilities and endpoints are per-model because they
// differ between models.
type Model struct {
	Name string // model name / Azure deployment name
	// Kind is the driver model family: "" | "generate" | "embed" |
	// "image" | "video" | "tts". Generation families are derived from
	// Outputs on write (image/video/text); embed/tts need it explicit.
	Kind string
	// Capabilities declares the model's input/output content kinds,
	// reasoning control (kind plus the canonical-to-wire effort map),
	// and hosted web search.
	Capabilities inference.ModelCapabilities
	// Endpoint binds this model to a per-model deployment address
	// (ByteDance Ark ep-xxx endpoint ids are account-scoped and map per
	// model in the profile); empty addresses the model by catalog name.
	Endpoint string
	// Responses marks Responses-API support for deepseek declared
	// models (required when the provider runs api: responses).
	Responses bool
	// Dimensions enables custom output dimensions (openai embed).
	Dimensions bool
	// EffortNone marks OpenAI/Azure generate models whose
	// reasoning.effort accepts "none" to disable reasoning; models
	// without it reject a reasoning_enabled=false request.
	EffortNone bool
}

// reasoningEffortOrder is the canonical effort ladder in ordinal order.
// The YAML writer uses it so effort maps serialize deterministically.
var reasoningEffortOrder = []inference.ReasoningEffort{
	inference.ReasoningMinimal,
	inference.ReasoningLow,
	inference.ReasoningMedium,
	inference.ReasoningHigh,
	inference.ReasoningXHigh,
}

// Instance is one configured inference endpoint: a provider type from
// the catalog (an optional base URL override), the models it serves,
// and its key. Several instances may share the same provider type
// (e.g. two DeepSeek endpoints); enabled instances form the router
// priority order.
type Instance struct {
	StableID string // stable identity across saves/reorders
	Type     string // catalog ID: deepseek | openai | ...
	Name     string // display label; empty derives "<type>-<n>"
	API      string // responses | chat (openai / openai-like)
	Endpoint string // base URL override; empty uses the driver default
	// ProviderSpec carries provider-owned spec options as an opaque
	// map (for example openai's chat_stream_options). The host never
	// interprets its contents; flowcraft's strict provider decode is
	// the final validator.
	ProviderSpec map[string]any
	Models       []Model
	KeySource    KeySource
	KeyValue     string // literal key (KeyLiteral) or store account (KeyKeychain)
	Enabled      bool
}

// DeploymentID returns the provider resource id for this instance.
// Rows saved through the settings page carry a stable identity, so the
// id ("<type>-<stableID>", e.g. "deepseek-inst-0a1b2c3d") survives
// reorders, edits, and deletions and the router's per-conversation
// model hints stay valid across config changes. n is the 1-based
// position used when no stable identity is present.
func (in Instance) DeploymentID(n int) string {
	if in.StableID != "" {
		return in.Type + "-" + in.StableID
	}
	return fmt.Sprintf("%s-%d", in.Type, n)
}

// ModelReasoning reports whether the model selected by hint
// ("<deployment-id>/<name>", empty = first enabled instance's first
// model) declares a reasoning capability. Drivers reject
// reasoning_effort / reasoning_enabled knobs for models without one, so
// callers must only send the knob when this returns true.
func (c InferenceConfig) ModelReasoning(hint string) bool {
	prov, name, ok := strings.Cut(hint, "/")
	var target Instance
	var targetName string
	found := false
	if !ok || strings.TrimSpace(prov) == "" || strings.TrimSpace(name) == "" {
		// No hint: the default policy target is the first enabled
		// instance's first model.
		for _, in := range c.Instances {
			if !in.Enabled || len(in.Models) == 0 {
				continue
			}
			target = in
			targetName = in.Models[0].Name
			found = true
			break
		}
	} else {
		for i, in := range c.Instances {
			if !in.Enabled {
				continue
			}
			if in.DeploymentID(i+1) == prov {
				target = in
				targetName = name
				found = true
				break
			}
		}
	}
	if !found {
		return false
	}
	for _, m := range target.Models {
		if m.Name == targetName && m.Capabilities.Reasoning.Kind != "" {
			return true
		}
	}
	return false
}

// ToPartKinds converts wire-form content kind strings to canonical
// message part kinds (unknown strings pass through so the provider
// layer, not the settings page, is the final arbiter).
func ToPartKinds(raw []string) []message.PartKind {
	if len(raw) == 0 {
		return nil
	}
	out := make([]message.PartKind, len(raw))
	for i, kind := range raw {
		out[i] = message.PartKind(kind)
	}
	return out
}

// PartKindStrings converts canonical part kinds back to wire strings.
func PartKindStrings(kinds []message.PartKind) []string {
	if len(kinds) == 0 {
		return nil
	}
	out := make([]string, len(kinds))
	for i, kind := range kinds {
		out[i] = string(kind)
	}
	return out
}

// ModelNames returns the non-empty model names in declaration order.
func (in Instance) ModelNames() []string {
	var names []string
	for _, m := range in.Models {
		if name := strings.TrimSpace(m.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// NewStableID returns a fresh instance identity. It is generated once
// per new row on save and persisted as the provider profile id (the
// only flowcraft-accepted carrier that stays with the instance through
// reorders and edits), so later saves can match rows by identity
// instead of guessing from fingerprints.
func NewStableID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand is not expected to fail; fall back to a
		// time-derived id so saving still works.
		return fmt.Sprintf("inst-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("inst-%x", b)
}

// InferenceConfig is one completed inference configuration: the
// enabled instances, in router priority order.
type InferenceConfig struct {
	Instances []Instance
}

// Enabled returns the instances that participate in routing, in order.
func (c InferenceConfig) Enabled() []Instance {
	var out []Instance
	for _, in := range c.Instances {
		if in.Enabled {
			out = append(out, in)
		}
	}
	return out
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
		return false, fmt.Errorf(
			"config: parse user config: %w", err)
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

// RouterConfigured reports whether the merged deployment document
// carries at least one router generate target. The embedded inference
// layer contributes a router retry shell with no pools until the user
// layer declares targets, so this distinguishes "inference
// is not configured yet" (an expected UI state) from a real router
// validation failure at build time.
func RouterConfigured(doc deploy.Document) (bool, error) {
	res, ok := doc.Resources["router"]
	if !ok {
		return false, nil
	}
	var settings struct {
		Generate []struct {
			Targets []json.RawMessage `json:"targets"`
		} `json:"generate"`
	}
	if len(res.Settings) > 0 {
		if err := json.Unmarshal(res.Settings, &settings); err != nil {
			return false, fmt.Errorf("config: decode merged router policy: %w", err)
		}
	}
	for _, pool := range settings.Generate {
		if len(pool.Targets) > 0 {
			return true, nil
		}
	}
	return false, nil
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
	if len(c.Instances) == 0 {
		return nil, errors.New("config: at least one enabled instance is required")
	}
	instances := make([]Instance, len(c.Instances))
	copy(instances, c.Instances)
	for i := range instances {
		in := &instances[i]
		prov, ok := ProviderByID(in.Type)
		if !ok {
			return nil, fmt.Errorf("config: unknown provider type %q", in.Type)
		}
		if err := normalizeModels(in, prov, i+1); err != nil {
			return nil, err
		}
		if prov.Azure && strings.TrimSpace(in.Endpoint) == "" {
			return nil, fmt.Errorf(
				"config: azure instance %d: endpoint is required", i+1)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# opencraft user configuration layer\n")
	fmt.Fprintf(&b, "# (~/.opencraft/config/opencraft.yaml; edited through the\n")
	fmt.Fprintf(&b, "# desktop settings page; last written %s).\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "#\n")
	fmt.Fprintf(&b, "# The FIXED inference wiring lives in the binary (embedded\n")
	fmt.Fprintf(&b, "# inference.yaml): the infer assembly and the router retry\n")
	fmt.Fprintf(&b, "# policy. This file carries the VARIABLE parts: one provider\n")
	fmt.Fprintf(&b, "# deployment per enabled instance (type + models + endpoint +\n")
	fmt.Fprintf(&b, "# capabilities + key), the infer dep wiring for those\n")
	fmt.Fprintf(&b, "# instances, and the router's generate targets in the\n")
	fmt.Fprintf(&b, "# enabled order (failures fall back to the next target).\n")
	fmt.Fprintf(&b, "# Resources, deps, and settings merge deeply across layers, so\n")
	fmt.Fprintf(&b, "# resources not managed here (MCP servers, sandbox policy,\n")
	fmt.Fprintf(&b, "# custom graphs) are preserved across settings writes.\n")
	fmt.Fprintf(&b, "version: v1\n")
	fmt.Fprintf(&b, "resources:\n")
	// One provider deployment per instance (disabled instances stay
	// declared so re-enabling them needs no re-entry); only enabled
	// instances join the infer deps and the router.
	var deps []string
	for i, in := range instances {
		prov, _ := ProviderByID(in.Type)
		id := in.DeploymentID(i + 1)
		fmt.Fprintf(&b, "  provider.%s:\n", id)
		fmt.Fprintf(&b, "    kind: inference.Provider\n")
		fmt.Fprintf(&b, "    impl: %s\n", prov.Impl)
		fmt.Fprintf(&b, "    settings:\n")
		fmt.Fprintf(&b, "      id: %s\n", id)
		fmt.Fprintf(&b, "      spec:\n")
		if prov.Azure {
			fmt.Fprintf(&b, "        endpoint: %s\n", yamlQuote(in.Endpoint))
		} else if in.Endpoint != "" {
			fmt.Fprintf(&b, "        base_url: %s\n", yamlQuote(in.Endpoint))
		}
		apiMode := in.API
		if apiMode == "" {
			apiMode = prov.API
		}
		if apiMode != "" && (prov.Impl == "openai" || prov.Impl == "deepseek") {
			fmt.Fprintf(&b, "        api: %s\n", yamlQuote(apiMode))
		}
		if prov.Impl == "openai" || prov.Impl == "deepseek" || prov.Impl == "azure" {
			fmt.Fprintf(&b, "        request_metadata:\n")
			fmt.Fprintf(&b, "          envelope: client_metadata\n")
		}
		if len(in.ProviderSpec) > 0 {
			if err := writeProviderSpecYAML(&b, in.ProviderSpec); err != nil {
				return nil, fmt.Errorf(
					"config: encode provider spec for %s: %w", id, err,
				)
			}
		}
		fmt.Fprintf(&b, "        models:\n")
		for _, m := range in.Models {
			kind := m.Kind
			outputs := m.Capabilities.Outputs
			if kind == "" {
				switch {
				case slices.Contains(outputs, message.PartVideo):
					kind = "video"
				case slices.Contains(outputs, message.PartImage):
					kind = "image"
				default:
					kind = "generate"
				}
			}
			inputs := m.Capabilities.Inputs
			if len(outputs) == 0 && kind == "generate" {
				// Text output is the generate family default; declared
				// models without it would fail driver validation.
				outputs = []message.PartKind{message.PartText}
			}
			if len(inputs) == 0 &&
				(kind == "generate" || kind == "image" || kind == "video") {
				// Undeclared inputs on a generation family default to
				// the base text modality (same rule as the text output
				// default). Explicit declarations are preserved
				// verbatim: capabilities are the declarer's truth, not
				// something the writer may rewrite.
				inputs = []message.PartKind{message.PartText}
			}
			fmt.Fprintf(&b, "          - name: %s\n", yamlQuote(m.Name))
			fmt.Fprintf(&b, "            kind: %s\n", yamlQuote(kind))
			fmt.Fprintf(&b, "            capabilities:\n")
			if len(outputs) > 0 {
				fmt.Fprintf(&b, "              outputs: [%s]\n",
					strings.Join(PartKindStrings(outputs), ", "))
			}
			if len(inputs) > 0 {
				fmt.Fprintf(&b, "              inputs: [%s]\n",
					strings.Join(PartKindStrings(inputs), ", "))
			}
			if !m.Capabilities.Reasoning.IsZero() {
				fmt.Fprintf(&b, "              reasoning:\n")
				fmt.Fprintf(&b, "                kind: %s\n",
					yamlQuote(string(m.Capabilities.Reasoning.Kind)))
				if len(m.Capabilities.Reasoning.EffortMap) > 0 {
					fmt.Fprintf(&b, "                effort_map:\n")
					for _, effort := range reasoningEffortOrder {
						mode, ok := m.Capabilities.Reasoning.EffortMap[effort]
						if !ok {
							continue
						}
						fmt.Fprintf(&b, "                  %s: %s\n",
							yamlQuote(string(effort)), yamlQuote(mode))
					}
				}
			}
			if m.Capabilities.HostedWebSearch {
				fmt.Fprintf(&b, "              hosted_web_search: true\n")
			}
			// DeepSeek's responses surface requires every declared model
			// to assert Responses-API support; derive it from the
			// provider's api mode so settings/plugin writes stay valid.
			if m.Responses || (prov.Impl == "deepseek" && apiMode == "responses") {
				fmt.Fprintf(&b, "            responses: true\n")
			}
			if m.Dimensions {
				fmt.Fprintf(&b, "            dimensions: true\n")
			}
			if m.EffortNone {
				fmt.Fprintf(&b, "            effort_none: true\n")
			}
		}
		fmt.Fprintf(&b, "      profiles:\n")
		fmt.Fprintf(&b, "        -")
		if in.StableID != "" {
			// The stable identity rides in the profile id: it is the
			// only provider-resource field flowcraft's strict settings
			// decode accepts that stays with the instance through
			// reorders and edits.
			fmt.Fprintf(&b, " id: %s\n", yamlQuote(in.StableID))
		} else {
			fmt.Fprintf(&b, "\n")
		}
		if prov.Impl == "bytedance" {
			var endpoints []Model
			for _, m := range in.Models {
				if strings.TrimSpace(m.Endpoint) != "" {
					endpoints = append(endpoints, m)
				}
			}
			if len(endpoints) > 0 {
				// Ark endpoint ids (ep-xxx) are account-scoped and bind
				// per model inside the profile, not at provider level.
				fmt.Fprintf(&b, "          endpoints:\n")
				for _, m := range endpoints {
					fmt.Fprintf(&b, "            %s: %s\n",
						yamlQuote(m.Name), yamlQuote(m.Endpoint))
				}
			}
		}
		fmt.Fprintf(&b, "          secrets:\n")
		fmt.Fprintf(&b, "            api_key: %s\n", instanceAPIKey(in, prov))
		if in.Enabled {
			deps = append(deps, id)
		}
	}
	if len(deps) > 0 {
		fmt.Fprintf(&b, "  infer:\n")
		fmt.Fprintf(&b, "    deps:\n")
		for _, id := range deps {
			fmt.Fprintf(&b, "      provider.%s: provider.%s\n", id, id)
		}
	}
	fmt.Fprintf(&b, "  router:\n")
	fmt.Fprintf(&b, "    settings:\n")
	fmt.Fprintf(&b, "      generate:\n")
	fmt.Fprintf(&b, "        - tier: default\n")
	fmt.Fprintf(&b, "          targets:\n")
	hasEnabled := false
	for i, in := range instances {
		if !in.Enabled {
			continue
		}
		for _, m := range in.Models {
			hasEnabled = true
			fmt.Fprintf(&b, "            - model:\n")
			fmt.Fprintf(&b, "                id:\n")
			fmt.Fprintf(&b, "                  provider: %s\n", in.DeploymentID(i+1))
			fmt.Fprintf(&b, "                  name: %s\n", yamlQuote(m.Name))
			if in.StableID != "" {
				fmt.Fprintf(&b, "                profile: %s\n", yamlQuote(in.StableID))
			}
		}
	}
	if !hasEnabled {
		// Keep the router block parseable (empty targets) so the
		// document stays valid while the user re-enables instances.
		fmt.Fprintf(&b, "          targets: []\n")
	}
	return []byte(b.String()), nil
}

// writeProviderSpecYAML renders an opaque provider spec map under the
// eight-space spec indentation used by InferenceYAML. yamlv4 sorts
// map keys and the fixed two-space indent keeps nested values aligned
// with the hand-written spec fields.
func writeProviderSpecYAML(
	b *strings.Builder,
	spec map[string]any,
) error {
	var buf bytes.Buffer
	enc := yamlv4.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(spec); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	for _, line := range strings.Split(
		strings.TrimRight(buf.String(), "\n"),
		"\n",
	) {
		b.WriteString("        ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return nil
}

// normalizeModels trims model names, fills empty ones with the
// provider's default model, rejects duplicates, and guarantees every
// instance declares at least one model. Empty names (the settings page
// prefill) and a fully empty list fall back to the provider default so
// the setup flow needs no explicit model entry; providers without a
// default (Azure) require an explicit name.
func normalizeModels(in *Instance, prov Provider, n int) error {
	if len(in.Models) == 0 {
		in.Models = []Model{{Name: prov.DefaultModel}}
	}
	models := make([]Model, 0, len(in.Models))
	seen := make(map[string]bool, len(in.Models))
	for _, m := range in.Models {
		m.Name = strings.TrimSpace(m.Name)
		if m.Name == "" {
			m.Name = prov.DefaultModel
		}
		if m.Name == "" {
			return fmt.Errorf(
				"config: instance %d (%s): model name is required", n, in.Type)
		}
		if seen[m.Name] {
			return fmt.Errorf(
				"config: instance %d (%s): duplicate model %q", n, in.Type, m.Name)
		}
		if m.Kind != "" {
			switch m.Kind {
			case "generate", "embed", "image", "video", "tts":
			default:
				return fmt.Errorf(
					"config: instance %d (%s): model %q has unknown kind %q",
					n, in.Type, m.Name, m.Kind)
			}
		}
		if err := m.Capabilities.Reasoning.Validate(); err != nil {
			return fmt.Errorf(
				"config: instance %d (%s): model %q reasoning: %w",
				n, in.Type, m.Name, err)
		}
		if m.EffortNone && prov.ID != "openai" && prov.ID != "azure" {
			return fmt.Errorf(
				"config: instance %d (%s): model %q: effort_none is only supported by openai/azure",
				n, in.Type, m.Name)
		}
		seen[m.Name] = true
		models = append(models, m)
	}
	in.Models = models
	return nil
}

// instanceAPIKey renders the profile secret value for one instance.
func instanceAPIKey(in Instance, prov Provider) string {
	if in.KeySource == KeyEnv {
		return "${env:" + prov.EnvVar + "}"
	}
	if in.KeySource == KeyKeychain {
		return "${secret:keychain." + in.KeyValue + "}"
	}
	return yamlQuote(in.KeyValue)
}

// providerOwnersFileName records, outside the flowcraft deployment
// document, which installed plugin owns each plugin-submitted
// inference instance. Flowcraft's strict provider settings cannot
// carry an extra owner field, so the stable provider profile id cannot
// be the sole ownership carrier once a plugin may submit several
// instances.
const providerOwnersFileName = "plugin-provider-owners.json"

// inferenceStateMu serializes inference config + owner writes. Both
// files together form one logical state (rows and their plugin owners);
// a plugin upsert, settings save and plugin disable/uninstall can run
// concurrently.
var inferenceStateMu sync.Mutex

// providerOwnersPath returns the sidecar path inside the user config
// directory.
func providerOwnersPath(configDir string) string {
	return filepath.Join(configDir, providerOwnersFileName)
}

// LoadProviderOwners returns the current plugin→instance ownership
// sidecar. A missing file is an empty map, not an error.
func LoadProviderOwners(configDir string) (map[string]string, error) {
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	return loadProviderOwnersLocked(configDir)
}

func loadProviderOwnersLocked(configDir string) (map[string]string, error) {
	data, err := os.ReadFile(providerOwnersPath(configDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", providerOwnersFileName, err)
	}
	if len(data) == 0 {
		return map[string]string{}, nil
	}
	owners := map[string]string{}
	if err := json.Unmarshal(data, &owners); err != nil {
		return nil, fmt.Errorf(
			"config: decode %s: %w", providerOwnersFileName, err)
	}
	return owners, nil
}

// DropProviderOwners removes every ownership row whose owning plugin
// id matches pluginID and reports how many were dropped. It is the
// owner-sidecar update used when a plugin is disabled or uninstalled
// without an inference config rewrite.
func DropProviderOwners(configDir, pluginID string) (int, error) {
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	owners, err := loadProviderOwnersLocked(configDir)
	if err != nil {
		return 0, err
	}
	dropped := 0
	for id, owner := range owners {
		if owner == pluginID {
			delete(owners, id)
			dropped++
		}
	}
	if dropped == 0 {
		return 0, nil
	}
	return dropped, saveProviderOwnersLocked(configDir, owners)
}

func saveProviderOwnersLocked(configDir string, owners map[string]string) error {
	if len(owners) == 0 {
		path := providerOwnersPath(configDir)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("config: remove %s: %w", providerOwnersFileName, err)
		}
		return nil
	}
	data, err := json.MarshalIndent(owners, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode %s: %w", providerOwnersFileName, err)
	}
	data = append(data, '\n')
	return writeFileAtomic(providerOwnersPath(configDir), data, 0o600)
}

// reconcileProviderOwners keeps only owner rows whose stable id still
// exists in cfg, so a settings save that drops a plugin row also drops
// its stale ownership record.
func reconcileProviderOwners(
	owners map[string]string,
	instances []Instance,
) map[string]string {
	alive := make(map[string]bool, len(instances))
	for _, in := range instances {
		if in.StableID != "" {
			alive[in.StableID] = true
		}
	}
	out := make(map[string]string, len(owners))
	for id, owner := range owners {
		if owner != "" && alive[id] {
			out[id] = owner
		}
	}
	return out
}

// managedProviderSpecKeys are the top-level provider spec keys the
// host writes from typed fields. Plugin provider_spec bags must not
// duplicate them.
var managedProviderSpecKeys = map[string]bool{
	"api":              true,
	"base_url":         true,
	"endpoint":         true,
	"models":           true,
	"request_metadata": true,
}

// ValidateProviderSpec checks an opaque plugin provider_spec bag
// before it is written. Reserved host-managed keys are rejected and
// provider-owned constraints (chat_stream_options is openai chat
// only) fail early; the flowcraft strict decode remains the final
// arbiter after the config is built.
func ValidateProviderSpec(typ, api string, spec map[string]any) error {
	for key := range spec {
		if managedProviderSpecKeys[key] {
			return fmt.Errorf(
				"provider spec key %q is managed by the host", key,
			)
		}
	}
	if _, ok := spec["chat_stream_options"]; ok {
		if !strings.EqualFold(typ, "openai") ||
			!strings.EqualFold(api, "chat") {
			return fmt.Errorf(
				"chat_stream_options requires an openai chat profile",
			)
		}
	}
	return nil
}

// writeInferenceLocked writes the user inference YAML. Callers hold
// inferenceStateMu.
func writeInferenceLocked(configDir string, cfg InferenceConfig) error {
	fresh, err := cfg.InferenceYAML()
	if err != nil {
		return err
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		managedResourceKeys(),
		map[string]bool{},
		true, // inference owns every provider.* resource
	)
	if err != nil {
		return err
	}
	return writeFileAtomic(
		filepath.Join(configDir, "opencraft.yaml"),
		merged,
		0o600,
	)
}

// WriteInference persists the inference configuration into the user
// configuration directory (opencraft.yaml), merging over the existing
// layer so resources the settings page does not manage (MCP servers,
// sandbox policy, custom graphs) are preserved. Plugin ownership rows
// whose instances survive the write are preserved; rows removed by the
// write drop their stale ownership records.
func WriteInference(configDir string, cfg InferenceConfig) error {
	return WriteInferenceOwned(configDir, cfg, nil)
}

// WriteInferenceOwned writes the inference configuration and replaces
// the plugin ownership sidecar while holding the config-state lock.
// owners is the full ownership map; nil preserves and reconciles the
// existing map against cfg.
func WriteInferenceOwned(
	configDir string,
	cfg InferenceConfig,
	owners map[string]string,
) error {
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	if err := writeInferenceLocked(configDir, cfg); err != nil {
		return err
	}
	if owners == nil {
		var err error
		owners, err = loadProviderOwnersLocked(configDir)
		if err != nil {
			return err
		}
	}
	return saveProviderOwnersLocked(
		configDir,
		reconcileProviderOwners(owners, cfg.Instances),
	)
}

// UpdateInferenceState runs one load-modify-write transaction over the
// inference config and its plugin ownership sidecar while holding the
// config-state lock. update receives the current rows and owners and
// returns the next state; returning nil owners preserves the current
// ownership sidecar (reconciled against the next rows). An empty next
// config removes the inference resources instead of writing an invalid
// empty document.
func UpdateInferenceState(
	configDir string,
	update func(
		cfg InferenceConfig,
		owners map[string]string,
	) (InferenceConfig, map[string]string, bool, error),
) error {
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	cfg, err := LoadInference(configDir)
	if err != nil {
		return err
	}
	owners, err := loadProviderOwnersLocked(configDir)
	if err != nil {
		return err
	}
	nextCfg, nextOwners, changed, err := update(cfg, owners)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if len(nextCfg.Instances) == 0 {
		if err := removeInferenceConfigLocked(configDir); err != nil {
			return err
		}
		return saveProviderOwnersLocked(configDir, map[string]string{})
	}
	if err := writeInferenceLocked(configDir, nextCfg); err != nil {
		return err
	}
	if nextOwners == nil {
		nextOwners = owners
	}
	return saveProviderOwnersLocked(
		configDir,
		reconcileProviderOwners(nextOwners, nextCfg.Instances),
	)
}

func removeInferenceConfigLocked(configDir string) error {
	fresh := []byte("version: v1\nresources: {}\n")
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		managedResourceKeys(),
		map[string]bool{},
		true, // inference owns every provider.* resource
	)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(
		filepath.Join(configDir, "opencraft.yaml"),
		merged,
		0o600,
	); err != nil {
		return err
	}
	return nil
}

// RemoveInferenceConfig drops every inference-managed resource
// (router, infer and each provider.*) from the user layer, returning
// the install to the unconfigured state. Non-inference resources are
// preserved. Used when the last provider is removed (e.g. SSO logout).
func RemoveInferenceConfig(configDir string) error {
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	if err := removeInferenceConfigLocked(configDir); err != nil {
		return err
	}
	return saveProviderOwnersLocked(configDir, map[string]string{})
}

// KeyRequest is one request row that needs a stored literal key
// ("leave empty to keep").
type KeyRequest struct {
	StableID string
	Type     string
	Name     string
	Models   []string
	Endpoint string
	API      string
}

// MatchStoredKeys assigns stored literal keys to request rows whose key
// was left blank. Only exact stable-id matches inherit keys: a row
// without a stable id is treated as new and cannot silently take an
// existing instance's key.
//
// claimed tracks old-instance indexes already inherited, preventing two
// rows from stealing the same key. Only literal and store-sourced
// keys are inherited (env-sourced keys are chosen explicitly via the
// request). The returned slice has one old-instance index per row (-1
// when that row could not be matched; ok is false then).
func MatchStoredKeys(
	existing []Instance,
	rows []KeyRequest,
	claimed map[int]bool,
) ([]int, bool) {
	matches := make([]int, len(rows))
	for i := range matches {
		matches[i] = -1
	}
	hasStoredKey := func(in Instance) bool {
		if in.KeyValue == "" {
			return false
		}
		return in.KeySource == KeyLiteral || in.KeySource == KeyKeychain
	}
	sameIdentity := func(row KeyRequest, in Instance) bool {
		return row.StableID != "" &&
			in.StableID == row.StableID &&
			in.Type == row.Type &&
			hasStoredKey(in)
	}

	unmatched := false
	for i, row := range rows {
		matched := false
		for idx, in := range existing {
			if claimed[idx] {
				continue
			}
			if sameIdentity(row, in) {
				claimed[idx] = true
				matches[i] = idx
				matched = true
				break
			}
		}
		if !matched {
			unmatched = true
		}
	}
	return matches, !unmatched
}

// managedResourceKeys returns the user-layer resources WriteInference
// replaces wholesale: the router policy and the infer dep wiring.
// Provider.* resources are managed separately by dropping every old
// provider key and keeping only the freshly generated ones.
func managedResourceKeys() map[string]bool {
	return map[string]bool{"router": true, "infer": true}
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
		Impl         string         `json:"impl"`
		ProviderSpec map[string]any `json:"-"`
		Settings     struct {
			ID       string `json:"id"`
			Profiles []struct {
				ID        string            `json:"id"`
				Endpoints map[string]string `json:"endpoints"`
				Secrets   struct {
					APIKey string `json:"api_key"`
				} `json:"secrets"`
			} `json:"profiles"`
			Spec struct {
				API      string `json:"api"`
				BaseURL  string `json:"base_url"`
				Endpoint string `json:"endpoint"`
				Models   []struct {
					Name         string `json:"name"`
					Kind         string `json:"kind"`
					Responses    bool   `json:"responses"`
					Dimensions   bool   `json:"dimensions"`
					Capabilities struct {
						Inputs          []string                      `json:"inputs"`
						Outputs         []string                      `json:"outputs"`
						Reasoning       inference.ReasoningCapability `json:"reasoning"`
						HostedWebSearch bool                          `json:"hosted_web_search"`
					} `json:"capabilities"`
					EffortNone bool `json:"effort_none"`
				} `json:"models"`
			} `json:"spec"`
		} `json:"settings"`
	}
	providers := make(map[string]instanceSettings, len(doc.Resources))
	for id, raw := range doc.Resources {
		if !strings.HasPrefix(id, "provider.") {
			continue
		}
		var res instanceSettings
		if err := yaml.Unmarshal(raw, &res); err != nil {
			return InferenceConfig{}, fmt.Errorf("config: parse %s: %w", id, err)
		}
		var specDoc struct {
			Settings struct {
				Spec map[string]any `json:"spec"`
			} `json:"settings"`
		}
		if err := yaml.Unmarshal(raw, &specDoc); err != nil {
			return InferenceConfig{}, fmt.Errorf(
				"config: parse %s spec: %w", id, err,
			)
		}
		extras := make(map[string]any)
		for key, value := range specDoc.Settings.Spec {
			if !managedProviderSpecKeys[key] {
				extras[key] = value
			}
		}
		if len(extras) > 0 {
			res.ProviderSpec = extras
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
		raw := providers[key]
		res := raw
		instID := res.Settings.ID
		if instID == "" {
			instID = strings.TrimPrefix(key, "provider.")
		}
		instType := providerTypeFromImpl(res.Impl)
		if instType == "" || instType != instanceTypeFromID(instID) {
			continue
		}
		in := Instance{Type: instType}
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
		if spec.Endpoint != "" {
			in.Endpoint = spec.Endpoint
		}
		in.ProviderSpec = res.ProviderSpec
		var endpoints map[string]string
		if len(res.Settings.Profiles) > 0 {
			endpoints = res.Settings.Profiles[0].Endpoints
		}
		for _, model := range spec.Models {
			m := Model{
				Name: model.Name,
				Kind: model.Kind,
				Capabilities: inference.ModelCapabilities{
					Inputs:          ToPartKinds(model.Capabilities.Inputs),
					Outputs:         ToPartKinds(model.Capabilities.Outputs),
					Reasoning:       model.Capabilities.Reasoning,
					HostedWebSearch: model.Capabilities.HostedWebSearch,
				},
				Responses:  model.Responses,
				Dimensions: model.Dimensions,
				EffortNone: model.EffortNone,
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

// providerTypeFromImpl maps a driver impl back to its catalog type id,
// or "" when the impl is not one of the catalog drivers.
func providerTypeFromImpl(impl string) string {
	for _, p := range Providers {
		if p.Impl == impl {
			return p.ID
		}
	}
	return ""
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
	dropProviderKeys bool,
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
	if len(oldNode.Content) == 0 {
		// An empty user layer (e.g. a file created with `touch`) has no
		// data to preserve: treat it like a missing layer and write the
		// fresh document, so first-time configuration cannot be blocked
		// by an empty file.
		return fresh, nil
	}
	if oldNode.Content[0].Kind != yamlv4.MappingNode {
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
			if replaceKeys[key] ||
				(dropProviderKeys && strings.HasPrefix(key, "provider.")) {
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
