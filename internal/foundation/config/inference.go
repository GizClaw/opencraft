package config

import (
	"crypto/rand"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"
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

// ServesText reports whether the model can serve a text-output generate
// request, the shape every chat turn has. The rule mirrors the router's
// own selection check on a declared model: an empty output list is
// undeclared rather than incompatible, while a declared list must
// contain text. Image/video/audio-only rows stay router targets for the
// generation tools and are never chat model choices.
func (m Model) ServesText() bool {
	if len(m.Capabilities.Outputs) == 0 {
		return true
	}
	return slices.Contains(m.Capabilities.Outputs, message.PartText)
}

// ModelLifecycle is the settings-page view of one model's discovery
// metadata. Only valid for deprecated or retired models: an active model
// must not carry retirement facts.
type ModelLifecycle struct {
	// Status is "deprecated" or "retired"; empty means active and is
	// never written.
	Status string `json:"status,omitempty"`
	// Replacement names the model that supersedes this one.
	ReplacementProvider string `json:"replacement_provider,omitempty"`
	ReplacementName     string `json:"replacement_name,omitempty"`
	// Notes is free-form guidance shown next to the deprecation.
	Notes string `json:"notes,omitempty"`
}

// IsZero reports whether the lifecycle carries nothing to write.
func (l ModelLifecycle) IsZero() bool {
	return l.Status == "" && l.ReplacementName == "" &&
		l.ReplacementProvider == "" && l.Notes == ""
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
// leaves opencraft round-trips, shared by the writer (renders them) and
// the loader (drops everything else). It is an allowlist rather than
// "keep every unknown key" on purpose: the drivers decode strictly, so a
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
	// Advanced carries the typed provider-level spec knobs the settings
	// page edits in its advanced section, and they are the only way to
	// reach a provider spec leaf: every key the four drivers accept is
	// modeled here, so no opaque provider-spec bag exists. Every field
	// is optional: an empty value leaves the driver default in place.
	// The writer maps each knob into the driver's own shape, which is
	// not uniform — OpenAI and Anthropic take an endpoint object,
	// ByteDance takes flat transport fields, and MiniMax names its
	// media origin separately.
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
	Routing      string            `json:"routing,omitempty"`
	Query        map[string]string `json:"query,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Organization string            `json:"organization,omitempty"`
	Project      string            `json:"project,omitempty"`
	Timeout      string            `json:"timeout,omitempty"`
	Region       string            `json:"region,omitempty"`
	// Auth names the credential transport for the OpenAI wire (Azure
	// authenticates with a header instead of a bearer token).
	AuthScheme string `json:"auth_scheme,omitempty"`
	AuthHeader string `json:"auth_header,omitempty"`
	// MetadataEnvelope is the top-level request-body field that
	// receives canonical request metadata; empty keeps the OpenAI wire
	// default, and the literal "-" disables forwarding.
	MetadataEnvelope string `json:"metadata_envelope,omitempty"`
	// HTTPRetries bounds wire-level retries inside one logical attempt.
	// Zero disables them; nil keeps the driver default.
	HTTPRetries *int `json:"http_retries,omitempty"`
	// ExtraBody carries provider body fields the driver does not model
	// (flowcraft's wire.extra_body): key to raw JSON value.
	ExtraBody map[string]string `json:"extra_body,omitempty"`
	// OpenAI wire dialect.
	// Store is the wire policy for the provider's server-side retention
	// field: "" keeps the driver default, "true"/"false" send that value,
	// and "omit" sends nothing for endpoints whose schema does not know
	// the field.
	Store                   string `json:"store,omitempty"`
	IncludeReasoningPayload *bool  `json:"include_reasoning_payload,omitempty"`
	ReasoningChannel        string `json:"reasoning_channel,omitempty"`
	ReasoningSummary        string `json:"reasoning_summary,omitempty"`
	Truncation              string `json:"truncation,omitempty"`
	ChatIncludeUsage        *bool  `json:"chat_include_usage,omitempty"`
	ChatIncludeObfuscation  *bool  `json:"chat_include_obfuscation,omitempty"`
	// ReasoningScope declares the verification scope this deployment's
	// reasoning traces belong to, replacing the derived
	// provider+model+credential scope. The OpenAI wire and Anthropic
	// take it under wire, ByteDance as a flat provider field.
	ReasoningScope string `json:"reasoning_scope,omitempty"`
	// VideoInput accepts video content blocks on a compatible Messages
	// endpoint (Anthropic's own schema has no video block).
	VideoInput bool `json:"video_input,omitempty"`
	// MiniMax names its media origin separately from the Messages
	// endpoint.
	MediaBaseURL            string `json:"media_base_url,omitempty"`
	VideoPollIntervalMillis *int   `json:"video_poll_interval_millis,omitempty"`
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
// ("<deployment-id>/<name>", empty = the router's default text target,
// the first enabled instance's first text-serving model) declares a
// reasoning capability. Drivers reject
// reasoning_effort / reasoning_enabled knobs for models without one, so
// callers must only send the knob when this returns true.
func (c InferenceConfig) ModelReasoning(hint string) bool {
	prov, name, ok := strings.Cut(hint, "/")
	var target Instance
	var targetName string
	found := false
	if !ok || strings.TrimSpace(prov) == "" || strings.TrimSpace(name) == "" {
		// No hint: the default policy target for a text request is the
		// first text-serving model of the first enabled instance. A
		// generation-only row (image/video/tts) is never that target, so
		// it must not decide the reasoning knob either.
		for _, in := range c.Instances {
			if !in.Enabled {
				continue
			}
			for _, m := range in.Models {
				if !m.ServesText() {
					continue
				}
				target = in
				targetName = m.Name
				found = true
				break
			}
			if found {
				break
			}
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
