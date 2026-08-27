package config

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// Model is one model served by an inference instance, with its own
// capabilities. A single instance may expose several models (e.g. two
// DeepSeek models sharing one endpoint/key); the capabilities are
// per-model because vision/reasoning/web-search support differs
// between models.
type Model struct {
	Name      string // model name / Azure deployment name
	Vision    bool   // capabilities.inputs: [image]
	Reasoning string // "" | "always" | "toggle" → capabilities.reasoning
	WebSearch bool   // capabilities.hosted_web_search: true
}

// Instance is one configured inference endpoint: a provider type from
// the catalog (an optional base URL override), the models it serves,
// and its key. Several instances may share the same provider type
// (e.g. two DeepSeek endpoints); enabled instances form the router
// priority order.
type Instance struct {
	StableID  string // stable identity across saves/reorders ("" on legacy configs)
	Type      string // catalog ID: deepseek | openai | ...
	Name      string // display label; empty derives "<type>-<n>"
	API       string // responses | chat (openai / openai-like)
	Endpoint  string // base URL override; empty uses the driver default
	Models    []Model
	KeySource KeySource
	KeyValue  string // literal key (KeyLiteral only)
	Enabled   bool
}

// DeploymentID returns the provider resource id for this instance.
// Rows saved through the settings page carry a stable identity, so the
// id ("<type>-<stableID>", e.g. "deepseek-inst-0a1b2c3d") survives
// reorders, edits, and deletions and the router's per-conversation
// model hints stay valid across config changes. Legacy rows without a
// stable identity fall back to the positional "<type>-<n>" form.
// n is the 1-based position of the instance in the config.
func (in Instance) DeploymentID(n int) string {
	if in.StableID != "" {
		return in.Type + "-" + in.StableID
	}
	return fmt.Sprintf("%s-%d", in.Type, n)
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
		if in.API != "" && prov.Impl == "openai" {
			fmt.Fprintf(&b, "        api: %s\n", yamlQuote(in.API))
		}
		fmt.Fprintf(&b, "        models:\n")
		for _, m := range in.Models {
			fmt.Fprintf(&b, "          - name: %s\n", yamlQuote(m.Name))
			fmt.Fprintf(&b, "            kind: generate\n")
			fmt.Fprintf(&b, "            capabilities:\n")
			fmt.Fprintf(&b, "              outputs: [text]\n")
			if m.Vision {
				fmt.Fprintf(&b, "              inputs: [image]\n")
			}
			if m.Reasoning != "" {
				fmt.Fprintf(&b, "              reasoning: %s\n", yamlQuote(m.Reasoning))
			}
			if m.WebSearch {
				fmt.Fprintf(&b, "              hosted_web_search: true\n")
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
			fmt.Fprintf(&b, "          secrets:\n")
		} else {
			fmt.Fprintf(&b, "\n          secrets:\n")
		}
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
	return yamlQuote(in.KeyValue)
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
// was left blank. Matching runs in three passes so reordering, deleting,
// and editing never silently swap keys:
//
//  1. Exact stable-id matches claim their old instance first, so a row
//     that was reordered or edited keeps its key no matter how its
//     fields changed;
//  2. Exact fingerprint matches (same type, name, model set, endpoint,
//     api)
//     claim their old instance first, across every row, so reordering
//     or deleting rows keeps each key on the right row (legacy configs
//     without stable ids);
//  3. Rows without a stable-id or fingerprint match take the first
//     unclaimed same-type instance with a literal key, in request
//     order, so a brand-new row (or one whose identity was lost) still
//     finds an available key instead of hard-failing.
//
// claimed tracks old-instance indexes already inherited, preventing two
// rows from stealing the same key. Only literal keys are inherited
// (env-sourced keys are chosen explicitly via the request). The
// returned slice has one old-instance index per row (-1 when that row
// could not be matched; ok is false then).
func MatchStoredKeys(
	existing []Instance,
	rows []KeyRequest,
	claimed map[int]bool,
) ([]int, bool) {
	matches := make([]int, len(rows))
	done := make([]bool, len(rows))
	for i := range matches {
		matches[i] = -1
	}
	hasLiteralKey := func(in Instance) bool {
		return in.KeySource == KeyLiteral && in.KeyValue != ""
	}
	sameIdentity := func(row KeyRequest, in Instance) bool {
		return row.StableID != "" &&
			in.StableID == row.StableID &&
			in.Type == row.Type &&
			hasLiteralKey(in)
	}
	fingerprint := func(row KeyRequest, in Instance) bool {
		return in.Type == row.Type &&
			in.Name == row.Name &&
			sameModelSet(row.Models, in.ModelNames()) &&
			in.Endpoint == row.Endpoint &&
			in.API == row.API &&
			hasLiteralKey(in)
	}

	// Pass 1: exact stable-id matches across all rows.
	for i, row := range rows {
		for idx, in := range existing {
			if claimed[idx] {
				continue
			}
			if sameIdentity(row, in) {
				claimed[idx] = true
				matches[i] = idx
				done[i] = true
				break
			}
		}
	}
	// Pass 2: exact fingerprints across all rows (legacy configs).
	for i, row := range rows {
		if done[i] {
			continue
		}
		for idx, in := range existing {
			if claimed[idx] {
				continue
			}
			if fingerprint(row, in) {
				claimed[idx] = true
				matches[i] = idx
				done[i] = true
				break
			}
		}
	}
	// Pass 3: leftover rows take the first unclaimed same-type key.
	for i, row := range rows {
		if done[i] {
			continue
		}
		for idx, in := range existing {
			if in.Type != row.Type || claimed[idx] || !hasLiteralKey(in) {
				continue
			}
			claimed[idx] = true
			matches[i] = idx
			done[i] = true
			break
		}
		if !done[i] {
			return matches, false
		}
	}
	return matches, true
}

// sameModelSet reports whether two model-name sets are equal
// (order-insensitive), used by the key fingerprint so editing the order
// of an instance's models does not lose its stored key.
func sameModelSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

// managedResourceKeys returns the resources WriteInference owns: every
// provider declaration, the router, and the infer dep wiring. Infer is
// managed even when Azure is not selected so a stale dep left by a
// previous Azure configuration is removed instead of referencing a
// deleted provider. Everything else in the user layer is preserved.
func managedResourceKeys(cfg InferenceConfig) map[string]bool {
	keys := map[string]bool{"router": true, "infer": true}
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
		Settings struct {
			ID       string `json:"id"`
			StableID string `json:"stable_id"`
			Profiles []struct {
				ID      string `json:"id"`
				Secrets struct {
					APIKey string `json:"api_key"`
				} `json:"secrets"`
			} `json:"profiles"`
			Spec struct {
				API      string `json:"api"`
				BaseURL  string `json:"base_url"`
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
	providers := make(map[string]instanceSettings, len(doc.Resources))
	for id, raw := range doc.Resources {
		if !strings.HasPrefix(id, "provider.") {
			continue
		}
		var res instanceSettings
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
		instType := instanceTypeFromID(instID)
		if instType == "" {
			continue
		}
		in := Instance{Type: instType}
		// The stable identity lives in the profile id (legacy configs
		// carried it in settings.stable_id, which flowcraft rejects).
		in.StableID = res.Settings.StableID
		if len(res.Settings.Profiles) > 0 {
			if pid := res.Settings.Profiles[0].ID; pid != "" {
				in.StableID = pid
			}
			k := res.Settings.Profiles[0].Secrets.APIKey
			if strings.HasPrefix(k, "${env:") && strings.HasSuffix(k, "}") {
				in.KeySource = KeyEnv
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
		for _, model := range spec.Models {
			m := Model{Name: model.Name, Reasoning: model.Capabilities.Reasoning}
			for _, input := range model.Capabilities.Inputs {
				if input == "image" {
					m.Vision = true
				}
			}
			m.WebSearch = model.Capabilities.HostedWebSearch
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
// type: a bare catalog id (legacy single-provider configs), a
// "<type>-<n>" instance id, or the stable "<type>-<stableID>" form.
func instanceTypeFromID(id string) string {
	if _, ok := ProviderByID(id); ok {
		return id
	}
	best := ""
	for _, p := range Providers {
		if strings.HasPrefix(id, p.ID+"-") && len(p.ID) > len(best) {
			best = p.ID
		}
	}
	return best
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
