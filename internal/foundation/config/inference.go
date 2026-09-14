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
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"
	yamlv4 "go.yaml.in/yaml/v4"
	"sigs.k8s.io/yaml"

	"github.com/GizClaw/opencraft/internal/foundation/compat"
)

// Inference wiring lives in the user configuration layer
// (~/.opencraft/config/opencraft.yaml), edited through the desktop
// settings page: every provider is registered into the infer assembly
// (Azure only when the user configures it, since it needs an endpoint
// and deployment). The user only supplies keys for the providers they
// have; the router policy lists the keyed providers in priority order
// with retry fallback, so routing is automatic.

// Provider is one inference driver a deployment can use. It carries
// only what identifies the driver and its conventional credential: how
// a deployment is addressed (endpoint, API surface, wire dialect) and
// which models it serves are deployment data, declared per instance.
type Provider struct {
	ID   string // deploy resource id (provider.<id>) and driver impl
	Impl string // driver impl registered in the runtime
	Name string // display name
	// EnvVar is the conventional API key environment variable, used as
	// the default for the env key source and as the hint shown next to
	// the key field.
	EnvVar string
	// ModelEndpoint marks drivers whose deployment binds models to
	// per-model endpoints (ByteDance Ark ep-xxx ids are account-scoped
	// and live in the profile), so the settings UI surfaces the field.
	ModelEndpoint bool
}

// Providers lists the drivers a deployment can be built from. There is
// deliberately no vendor table: which endpoint and which models a
// provider serves is deployment configuration, so the settings page
// asks for it per instance instead of shipping a list that goes stale.
var Providers = []Provider{
	{ID: "openai", Impl: "openai", Name: "OpenAI", EnvVar: "OPENAI_API_KEY"},
	{ID: "anthropic", Impl: "anthropic", Name: "Anthropic", EnvVar: "ANTHROPIC_API_KEY"},
	{ID: "bytedance", Impl: "bytedance", Name: "Bytedance", EnvVar: "ARK_API_KEY", ModelEndpoint: true},
	{ID: "minimax", Impl: "minimax", Name: "Minimax Media", EnvVar: "MINIMAX_API_KEY"},
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

// ProviderFor resolves the descriptor that serves one instance: the
// catalog preset Type names, or — for a plugin-declared provider that
// is not in the preset list — one synthesized from the instance's
// explicit driver. A provider with neither a known Type nor a driver
// cannot be written.
func ProviderFor(in Instance) (Provider, bool) {
	if prov, ok := ProviderByID(in.Type); ok {
		return prov, true
	}
	if strings.TrimSpace(in.Driver) == "" {
		return Provider{}, false
	}
	prov := Provider{
		ID:   in.Type,
		Impl: strings.TrimSpace(in.Driver),
		Name: in.Name,
	}
	if prov.Name == "" {
		prov.Name = in.Type
	}
	return prov, true
}

// OpenAIWire reports whether the provider is served by the OpenAI
// wire-family driver, which owns the generate surface, the endpoint
// object, the metadata envelope and the request-metadata lowering.
func (p Provider) OpenAIWire() bool { return p.Impl == "openai" }

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
// mirrors flowcraft's model.ModelCapabilities verbatim so no
// capability is lost across the config boundary. A single instance may
// expose several models (e.g. two DeepSeek models sharing one
// endpoint/key); capabilities and endpoints are per-model because they
// differ between models.
type Model struct {
	Name string // model name / Azure deployment name
	// Kind is the driver model family: "" | "generate" | "image" |
	// "video" | "tts". Generation families are derived from
	// Outputs on write (image/video/text); tts needs it explicit.
	// Embedding models are no longer configurable.
	Kind string
	// Capabilities declares the model's input/output content kinds,
	// reasoning control (kind plus the canonical-to-wire effort map),
	// and hosted web search.
	Capabilities model.ModelCapabilities
	// Endpoint binds this model to a per-model deployment address
	// (ByteDance Ark ep-xxx endpoint ids are account-scoped and map per
	// model in the profile); empty addresses the model by catalog name.
	Endpoint string
	// Limits declares numeric capacity limits (input/output context in
	// tokens) for this model. Nil fields leave the driver catalog value
	// untouched for built-in models and publish nothing for deployments
	// without a catalog (azure); declaring a value overrides it.
	Limits model.ModelLimits
	// Lifecycle is the model's discovery metadata: deprecation,
	// retirement and the model that replaces it. Empty means active.
	Lifecycle ModelLifecycle
	// DriverFields carries driver-specific model leaves opencraft does
	// not model, verbatim (ByteDance's max_resolution and Seedance
	// parameter matrix, MiniMax's wire_model and video surface, ...).
	// The keys a driver owns are the driver's business; the host only
	// refuses the ones it writes itself.
	DriverFields map[string]any
}

// ModelLifecycle is the settings-page view of one model's discovery
// metadata. Only valid for deprecated or retired models: an active model
// must not carry retirement facts.
type ModelLifecycle struct {
	// Status is "deprecated" or "retired"; empty means active and is
	// never written.
	Status string
	// Replacement names the model that supersedes this one.
	ReplacementProvider string
	ReplacementName     string
	// Notes is free-form guidance shown next to the deprecation.
	Notes string
}

// IsZero reports whether the lifecycle carries nothing to write.
func (l ModelLifecycle) IsZero() bool {
	return l.Status == "" && l.ReplacementName == "" &&
		l.ReplacementProvider == "" && l.Notes == ""
}

// reasoningEffortOrder is the canonical effort ladder in ordinal order.
// The YAML writer uses it so effort maps serialize deterministically.
var reasoningEffortOrder = []model.ReasoningEffort{
	model.ReasoningMinimal,
	model.ReasoningLow,
	model.ReasoningMedium,
	model.ReasoningHigh,
	model.ReasoningXHigh,
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
	// Driver names the flowcraft driver impl for a provider that is not
	// in the built-in preset list (a plugin-declared vendor). Empty uses
	// the preset that Type names.
	Driver   string
	API      string // responses | chat (openai / openai-like)
	Endpoint string // base URL override; empty uses the driver default
	// ProviderSpec carries provider-owned spec options as an opaque
	// map (for example openai's chat_stream_options). The host never
	// interprets its contents; flowcraft's strict provider decode is
	// the final validator.
	ProviderSpec map[string]any
	// Advanced carries the typed provider-level spec knobs the settings
	// page edits in its advanced section. Every field is optional: an
	// empty value leaves the driver default in place. The writer maps
	// each knob into the driver's own shape, which is not uniform —
	// OpenAI and Anthropic take an endpoint object, ByteDance takes
	// flat transport fields, and MiniMax names its media origin
	// separately.
	Advanced  InstanceAdvanced
	Models    []Model
	KeySource KeySource
	KeyValue  string // literal key (KeyLiteral) or store account (KeyKeychain)
	Enabled   bool
}

// InstanceAdvanced is the typed view of one instance's provider-level
// spec knobs — everything the settings page edits below the basic
// endpoint + models + key flow. The zero value means "driver
// defaults"; each field is only written when it is set, so a saved
// document never pins a value the user did not choose. Fields are
// grouped by the driver that consumes them.
type InstanceAdvanced struct {
	// Endpoint transport. Routing, Organization and the endpoint query
	// belong to the OpenAI wire family (Azure deployment routing,
	// OpenAI-Organization/Project headers); Query, Headers, Project,
	// Timeout and Region belong to ByteDance, which takes them as flat
	// provider-level fields.
	Routing      string
	Query        map[string]string
	Headers      map[string]string
	Organization string
	Project      string
	Timeout      string
	Region       string
	// Auth names the credential transport for the OpenAI wire (Azure
	// authenticates with a header instead of a bearer token).
	AuthScheme string
	AuthHeader string
	// MetadataEnvelope is the top-level request-body field that
	// receives canonical request metadata; empty keeps the OpenAI wire
	// default, and the literal "-" disables forwarding.
	MetadataEnvelope string
	// HTTPRetries bounds wire-level retries inside one logical attempt.
	// Zero disables them; nil keeps the driver default.
	HTTPRetries *int
	// ExtraBody carries provider body fields the driver does not model
	// (flowcraft's wire.extra_body): key to raw JSON value.
	ExtraBody map[string]string
	// OpenAI wire dialect.
	// Store is the wire policy for the provider's server-side retention
	// field: "" keeps the driver default, "true"/"false" send that value,
	// and "omit" sends nothing for endpoints whose schema does not know
	// the field.
	Store                   string
	IncludeReasoningPayload *bool
	ReasoningChannel        string
	ReasoningSummary        string
	Truncation              string
	ChatIncludeUsage        *bool
	ChatIncludeObfuscation  *bool
	// VideoInput accepts video content blocks on a compatible Messages
	// endpoint (Anthropic's own schema has no video block).
	VideoInput bool
	// MiniMax names its media origin separately from the Messages
	// endpoint.
	MediaBaseURL            string
	VideoPollIntervalMillis *int
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
	// Router is the generate retry policy the settings page owns. The
	// zero value means "use the defaults": the writer then emits the
	// historical shell (two attempts, fall back to the next target).
	Router RouterPolicy
}

// RouterPolicy is the router's generate retry policy. It is the one
// router knob the settings page owns; the targets themselves are
// derived from the instance list, and the tier layout stays fixed
// until there is a product need for more than one tier.
type RouterPolicy struct {
	// MaxAttempts bounds attempts per target, including the first. A
	// value <= 0 means "use the default".
	MaxAttempts int
	// FallbackOnRetryExhausted moves on to the next target in the tier
	// once a target's attempts are spent.
	FallbackOnRetryExhausted bool
}

// DefaultRouterPolicy is the retry shell OpenCraft has always written.
func DefaultRouterPolicy() RouterPolicy {
	return RouterPolicy{MaxAttempts: 2, FallbackOnRetryExhausted: true}
}

// withDefaults fills in an unset attempt count. A policy that names an
// attempt count keeps its fallback flag verbatim, so switching the
// fallback off stays expressible.
func (p RouterPolicy) withDefaults() RouterPolicy {
	if p.MaxAttempts <= 0 {
		return DefaultRouterPolicy()
	}
	return p
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

// InferenceNeeded reports whether the user configuration layer carries
// no enabled inference wiring: no file at all, or a router with no
// generate targets. It answers the same question RouterConfigured
// answers on the merged document, from the user layer alone.
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
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, fmt.Errorf(
			"config: parse user config: %w", err)
	}
	raw, ok := doc.Resources["router"]
	if !ok {
		return true, nil
	}
	var res struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, fmt.Errorf("config: parse user router: %w", err)
	}
	targeted, err := routerHasTargets(res.Settings)
	if err != nil {
		return false, err
	}
	return !targeted, nil
}

// RouterConfigured reports whether the merged deployment document
// carries at least one router generate target. The generated user
// layer always declares the router, so an empty target list
// distinguishes "inference is not configured yet" (an expected UI
// state) from a real router validation failure at build time.
func RouterConfigured(doc deploy.Document) (bool, error) {
	res, ok := doc.Resources["router"]
	if !ok {
		return false, nil
	}
	return routerHasTargets(res.Settings)
}

// routerHasTargets reports whether one router settings document
// declares at least one generate target.
func routerHasTargets(settings json.RawMessage) (bool, error) {
	if len(settings) == 0 {
		return false, nil
	}
	var policy struct {
		Generate []struct {
			Targets []json.RawMessage `json:"targets"`
		} `json:"generate"`
	}
	if err := json.Unmarshal(settings, &policy); err != nil {
		return false, fmt.Errorf("config: decode router policy: %w", err)
	}
	for _, pool := range policy.Generate {
		if len(pool.Targets) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// InferenceYAML renders the user configuration layer
// (~/.opencraft/config/opencraft.yaml). The whole inference wiring is
// host-owned and generated here: one provider deployment per instance
// (driver, endpoint, wire dialect, declared models, key profile), the
// infer assembly over those providers, and the router — retry shell
// plus the generate targets derived from the enabled instances in
// priority order. Nothing about inference lives in the embedded
// layers any more, so this document is the single description of what
// the deployment serves.
func (c InferenceConfig) InferenceYAML() ([]byte, error) {
	return c.inferenceYAMLAt(time.Now())
}

// writeProviderSpec renders one provider's `spec:` body — the basic
// endpoint plus every advanced knob the instance sets — and returns
// the top-level spec keys it wrote. The shape follows the driver:
// OpenAI and Anthropic take an endpoint object, ByteDance takes flat
// transport fields, and MiniMax names its media origin separately.
// Callers pass the returned set to writeProviderSpecYAML so the opaque
// provider-spec bag can never duplicate a key this writer emitted.
func writeProviderSpec(
	b *strings.Builder, prov Provider, in Instance, apiMode string,
) (map[string]bool, error) {
	adv := in.Advanced
	written := make(map[string]bool, 8)
	if apiMode != "" && prov.OpenAIWire() {
		fmt.Fprintf(b, "        api: %s\n", yamlQuote(apiMode))
		written["api"] = true
	}
	baseURL := strings.TrimSpace(in.Endpoint)
	switch prov.Impl {
	case "bytedance":
		if baseURL != "" {
			fmt.Fprintf(b, "        base_url: %s\n", yamlQuote(baseURL))
			written["base_url"] = true
		}
		for _, key := range []struct {
			name  string
			value string
		}{
			{"region", adv.Region},
			{"project", adv.Project},
			{"timeout", adv.Timeout},
		} {
			if key.value == "" {
				continue
			}
			fmt.Fprintf(b, "        %s: %s\n", key.name, yamlQuote(key.value))
			written[key.name] = true
		}
		writeStringMap(b, "        ", "headers", adv.Headers, written)
		writeStringMap(b, "        ", "query", adv.Query, written)
	case "minimax":
		if baseURL != "" {
			fmt.Fprintf(b, "        media_base_url: %s\n", yamlQuote(baseURL))
			written["media_base_url"] = true
		}
	default:
		// OpenAI wire family and Anthropic: one endpoint object.
		routing := strings.TrimSpace(adv.Routing)
		endpointKeys := baseURL != "" || routing != "" || adv.Organization != "" ||
			adv.Project != "" || adv.Timeout != "" ||
			len(adv.Query) > 0 || len(adv.Headers) > 0
		if endpointKeys {
			fmt.Fprintf(b, "        endpoint:\n")
			written["endpoint"] = true
			if baseURL != "" {
				fmt.Fprintf(b, "          base_url: %s\n", yamlQuote(baseURL))
			}
			for _, key := range []struct {
				name  string
				value string
			}{
				{"routing", routing},
				{"organization", adv.Organization},
				{"project", adv.Project},
				{"timeout", adv.Timeout},
			} {
				if key.value == "" {
					continue
				}
				fmt.Fprintf(b, "          %s: %s\n", key.name, yamlQuote(key.value))
			}
			writeStringMap(b, "          ", "headers", adv.Headers, nil)
			writeStringMap(b, "          ", "query", adv.Query, nil)
		}
		scheme := strings.TrimSpace(adv.AuthScheme)
		if scheme != "" {
			fmt.Fprintf(b, "        auth:\n")
			written["auth"] = true
			fmt.Fprintf(b, "          scheme: %s\n", yamlQuote(scheme))
			header := strings.TrimSpace(adv.AuthHeader)
			if header != "" {
				fmt.Fprintf(b, "          header: %s\n", yamlQuote(header))
			}
		}
	}
	// Wire dialect.
	channel := strings.TrimSpace(adv.ReasoningChannel)
	switch prov.Impl {
	case "openai":
		wireKeys := channel != "" || adv.VideoInput ||
			adv.Store != "" ||
			adv.IncludeReasoningPayload != nil || adv.ReasoningSummary != "" ||
			adv.Truncation != "" || adv.ChatIncludeUsage != nil ||
			adv.ChatIncludeObfuscation != nil
		if wireKeys {
			fmt.Fprintf(b, "        wire:\n")
			written["wire"] = true
			if adv.VideoInput {
				fmt.Fprintf(b, "          video_input: true\n")
			}
			if adv.Store != "" {
				fmt.Fprintf(b, "          store: %s\n", adv.Store)
			}
			if adv.IncludeReasoningPayload != nil {
				fmt.Fprintf(b, "          include_reasoning_payload: %t\n",
					*adv.IncludeReasoningPayload)
			}
			if channel != "" {
				fmt.Fprintf(b, "          reasoning_channel: %s\n",
					yamlQuote(channel))
			}
			if adv.ReasoningSummary != "" {
				fmt.Fprintf(b, "          reasoning_summary: %s\n",
					yamlQuote(adv.ReasoningSummary))
			}
			if adv.Truncation != "" {
				fmt.Fprintf(b, "          truncation: %s\n",
					yamlQuote(adv.Truncation))
			}
			if len(adv.ExtraBody) > 0 {
				keys := make([]string, 0, len(adv.ExtraBody))
				for key := range adv.ExtraBody {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				fmt.Fprintf(b, "          extra_body:\n")
				for _, key := range keys {
					value := strings.TrimSpace(adv.ExtraBody[key])
					if !json.Valid([]byte(value)) {
						return nil, fmt.Errorf(
							"config: wire.extra_body %q is not a JSON value",
							key,
						)
					}
					fmt.Fprintf(b, "            %s: %s\n",
						yamlQuote(key), value)
				}
			}
			if adv.ChatIncludeUsage != nil || adv.ChatIncludeObfuscation != nil {
				fmt.Fprintf(b, "          chat_stream_options:\n")
				if adv.ChatIncludeUsage != nil {
					fmt.Fprintf(b, "            include_usage: %t\n",
						*adv.ChatIncludeUsage)
				}
				if adv.ChatIncludeObfuscation != nil {
					fmt.Fprintf(b, "            include_obfuscation: %t\n",
						*adv.ChatIncludeObfuscation)
				}
			}
		}
	case "anthropic":
		if adv.VideoInput {
			fmt.Fprintf(b, "        wire:\n")
			fmt.Fprintf(b, "          video_input: true\n")
			written["wire"] = true
		}
	}
	if prov.OpenAIWire() {
		envelope := adv.MetadataEnvelope
		if envelope == "" {
			envelope = "client_metadata"
		}
		// The empty object disables metadata forwarding; "-" is the
		// settings page's "off" choice, and the key is written even
		// then so the opt-out round-trips instead of reading back as
		// "use the default".
		if envelope == "-" {
			fmt.Fprintf(b, "        request_metadata: {}\n")
		} else {
			fmt.Fprintf(b, "        request_metadata:\n")
			fmt.Fprintf(b, "          envelope: %s\n", yamlQuote(envelope))
		}
		written["request_metadata"] = true
	}
	if adv.HTTPRetries != nil {
		fmt.Fprintf(b, "        http_retries: %d\n", *adv.HTTPRetries)
		written["http_retries"] = true
	}
	if prov.Impl == "bytedance" && adv.VideoPollIntervalMillis != nil {
		fmt.Fprintf(b, "        video_poll_interval_millis: %d\n",
			*adv.VideoPollIntervalMillis)
		written["video_poll_interval_millis"] = true
	}
	return written, nil
}

// writeStringMap renders one string map at the given indentation,
// marking the key as written when it has entries.
func writeStringMap(
	b *strings.Builder,
	indent, key string,
	values map[string]string,
	written map[string]bool,
) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(b, "%s%s:\n", indent, key)
	for _, k := range keys {
		fmt.Fprintf(b, "%s  %s: %s\n", indent, yamlQuote(k), yamlQuote(values[k]))
	}
	if written != nil {
		written[key] = true
	}
}

// modelDriverFieldKeys are the model-entry keys opencraft writes itself;
// a driver-specific bag must not restate them.
var modelDriverFieldKeys = map[string]bool{
	"name":         true,
	"kind":         true,
	"capabilities": true,
	"limits":       true,
	"lifecycle":    true,
}

// modelDriverFieldsByImpl is the allowlist of driver-specific model
// leaves opencraft round-trips. It is an allowlist rather than "keep
// every unknown key" on purpose: the drivers decode strictly, so a
// stale or misspelled key would otherwise be preserved forever and then
// rejected at build time with a message about a field the user never
// wrote. Adding a driver fact means adding it here.
var modelDriverFieldsByImpl = map[string]map[string]bool{
	"bytedance": {"max_resolution": true, "video": true},
	"minimax":   {"wire_model": true, "video": true},
}

// allowedModelDriverField reports whether one driver-specific model leaf
// survives the settings-page round trip for this driver.
func allowedModelDriverField(impl, key string) bool {
	return modelDriverFieldsByImpl[impl][key]
}

// writeModelDriverFields renders the driver-specific leaves of one model
// entry verbatim, so a driver can grow declaration facts (resolution
// caps, wire-model aliases, parameter matrices) without opencraft having
// to model each one.
func writeModelDriverFields(
	b *strings.Builder, modelName, impl string, fields map[string]any,
) error {
	if len(fields) == 0 {
		return nil
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf(
			"config: encode driver fields for model %s: %w", modelName, err,
		)
	}
	ordered := map[string]any{}
	if err := json.Unmarshal(raw, &ordered); err != nil {
		return fmt.Errorf(
			"config: decode driver fields for model %s: %w", modelName, err,
		)
	}
	keys := make([]string, 0, len(ordered))
	for key := range ordered {
		if modelDriverFieldKeys[key] {
			return fmt.Errorf(
				"config: model %s driver field %q is written by the host",
				modelName, key,
			)
		}
		if !allowedModelDriverField(impl, key) {
			return fmt.Errorf(
				"config: model %s driver field %q is not declared by the %s "+
					"driver", modelName, key, impl,
			)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, err := yaml.Marshal(ordered[key])
		if err != nil {
			return fmt.Errorf(
				"config: encode driver field %s of model %s: %w",
				key, modelName, err,
			)
		}
		lines := strings.Split(strings.TrimRight(string(value), "\n"), "\n")
		if len(lines) == 1 {
			fmt.Fprintf(b, "            %s: %s\n", key, lines[0])
			continue
		}
		// A composite value nests one level under its key; the marshaled
		// text carries no leading indent of its own.
		fmt.Fprintf(b, "            %s:\n", key)
		for _, line := range lines {
			fmt.Fprintf(b, "              %s\n", line)
		}
	}
	return nil
}

// writeProviderSpecYAML renders an opaque provider spec map under the
// eight-space spec indentation used by InferenceYAML. yamlv4 sorts
// map keys and the fixed two-space indent keeps nested values aligned
// with the hand-written spec fields.
func writeProviderSpecYAML(
	b *strings.Builder,
	spec map[string]any,
	skip map[string]bool,
) error {
	if len(skip) > 0 {
		filtered := make(map[string]any, len(spec))
		for key, value := range spec {
			if skip[key] {
				continue
			}
			filtered[key] = value
		}
		spec = filtered
	}
	if len(spec) == 0 {
		return nil
	}
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

// normalizeModels trims model names, rejects duplicates, and guarantees
// every instance declares at least one named model. There is no model
// table to fall back on: the deployment says which models it serves, so
// a nameless row is a validation error rather than a silent default.
func normalizeModels(in *Instance, prov Provider, n int) error {
	if len(in.Models) == 0 {
		in.Models = []Model{{}}
	}
	models := make([]Model, 0, len(in.Models))
	seen := make(map[string]bool, len(in.Models))
	for _, m := range in.Models {
		m.Name = strings.TrimSpace(m.Name)
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
			case "generate", "image", "video", "tts":
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
		if err := m.Limits.Validate(); err != nil {
			return fmt.Errorf(
				"config: instance %d (%s): model %q limits: %w",
				n, in.Type, m.Name, err)
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

// adoptLegacyProviderOwners records ownership for inference rows
// written under the single-instance plugin contract that predates the
// ownership sidecar. In that scheme the host only accepted instance
// ids equal to the calling plugin id and the key reference lived
// inside the plugin's secret namespace, so a row matching both shapes
// could only have been created by the plugin with that id. Rows like
// this carry no sidecar record after an upgrade; without adoption the
// plugin can neither replace nor remove its own stale deployment,
// leaving duplicate inference entries behind.
func adoptLegacyProviderOwners(cfg InferenceConfig, owners map[string]string) {
	for _, in := range cfg.Instances {
		if _, ok := owners[in.StableID]; ok {
			continue
		}
		if in.KeySource == KeyKeychain &&
			compat.LegacyPluginKeyRef(in.StableID, in.KeyValue) {
			owners[in.StableID] = in.StableID
		}
	}
}

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

// LoadProviderOwners returns the current plugin→instance ownership,
// including legacy rows created under the single-instance plugin
// contract (see adoptLegacyProviderOwners). A missing sidecar file is
// an empty map, not an error.
func LoadProviderOwners(configDir string) (map[string]string, error) {
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	owners, err := loadProviderOwnersLocked(configDir)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadInference(configDir)
	if err != nil {
		return nil, err
	}
	adoptLegacyProviderOwners(cfg, owners)
	return owners, nil
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
	"auth":             true,
	"endpoint":         true,
	"models":           true,
	"request_metadata": true,
	"wire":             true,
}

// hostManagedSpecKey reports whether the host writes one provider spec
// key itself. It merges the local write contract with the keys the
// compat layer retired, so a stale key never rides the opaque bag back
// into a document the drivers reject.
func hostManagedSpecKey(key string) bool {
	return managedProviderSpecKeys[key] || compat.RetiredProviderSpecKeys[key]
}

// ValidateProviderSpec checks an opaque plugin provider_spec bag
// before it is written. Reserved host-managed keys are rejected and
// provider-owned constraints (chat_stream_options is openai chat
// only) fail early; the flowcraft strict decode remains the final
// arbiter after the config is built. prov is the descriptor that will
// serve the profile, so a plugin-declared driver is judged by its own
// impl rather than by a preset id.
func ValidateProviderSpec(prov Provider, api string, spec map[string]any) error {
	for key := range spec {
		if hostManagedSpecKey(key) {
			return fmt.Errorf(
				"provider spec key %q is managed by the host", key,
			)
		}
	}
	if _, ok := spec["chat_stream_options"]; ok {
		if !prov.OpenAIWire() || !strings.EqualFold(api, "chat") {
			return fmt.Errorf(
				"chat_stream_options requires an OpenAI-wire chat profile",
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
	adoptLegacyProviderOwners(cfg, owners)
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
	adoptLegacyProviderOwners(cfg, owners)
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

// MigrateUserInferenceConfig rewrites the user inference document when
// it still carries a shape the canonical writer drops (see
// compat.UserLayerShapes). The rewrite re-emits the document from the
// typed configuration, so the rest of the configuration survives and
// non-inference resources are untouched by UpdateInferenceState.
// changed reports whether a rewrite happened.
func MigrateUserInferenceConfig(configDir string) (changed bool, err error) {
	path := filepath.Join(configDir, "opencraft.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("config: read inference migration source: %w", err)
	}
	if len(compat.ShapeNeedsRewrite(data)) == 0 {
		return false, nil
	}
	if err := UpdateInferenceState(configDir, func(
		cfg InferenceConfig,
		owners map[string]string,
	) (InferenceConfig, map[string]string, bool, error) {
		return cfg, owners, true, nil
	}); err != nil {
		return false, fmt.Errorf("config: migrate user inference document: %w", err)
	}
	return true, nil
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
		Truncation              string                     `json:"truncation"`
		VideoInput              bool                       `json:"video_input"`
		ExtraBody               map[string]json.RawMessage `json:"extra_body"`
		ChatStreamOptions       struct {
			IncludeUsage       *bool `json:"include_usage"`
			IncludeObfuscation *bool `json:"include_obfuscation"`
		} `json:"chat_stream_options"`
	} `json:"wire"`
	HTTPRetries  *int              `json:"http_retries"`
	Region       string            `json:"region"`
	Project      string            `json:"project"`
	Timeout      string            `json:"timeout"`
	Headers      map[string]string `json:"headers"`
	Query        map[string]string `json:"query"`
	MediaBaseURL string            `json:"media_base_url"`
	VideoPollMS  *int              `json:"video_poll_interval_millis"`
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
		Truncation:              spec.Wire.Truncation,
		ChatIncludeUsage:        spec.Wire.ChatStreamOptions.IncludeUsage,
		ChatIncludeObfuscation:  spec.Wire.ChatStreamOptions.IncludeObfuscation,
		VideoInput:              spec.Wire.VideoInput,
		MediaBaseURL:            spec.MediaBaseURL,
		VideoPollIntervalMillis: spec.VideoPollMS,
	}
	// ByteDance takes these flat instead of under endpoint.
	if spec.Region != "" || spec.Project != "" || spec.Timeout != "" ||
		len(spec.Headers) > 0 || len(spec.Query) > 0 {
		adv.Region = spec.Region
		adv.Project = spec.Project
		adv.Timeout = spec.Timeout
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
			if !hostManagedSpecKey(key) {
				extras[key] = value
			}
		}
		// chat_stream_options moved under wire when the OpenAI wire
		// family gained one driver; surface it again as the flat
		// provider-spec key the settings page round-trips.
		if wire, ok := specDoc.Settings.Spec["wire"].(map[string]any); ok {
			if options, ok := wire["chat_stream_options"]; ok {
				extras["chat_stream_options"] = options
			}
		}
		if len(extras) > 0 {
			res.ProviderSpec = extras
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
		in.ProviderSpec = res.ProviderSpec
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
