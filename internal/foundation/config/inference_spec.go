package config

import (
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/GizClaw/flowcraft/core/inference/model"
)

// The inference row contract.
//
// One JSON shape describes one inference deployment for every producer:
// the desktop settings page submits a list of InstanceSpec, and a
// capability plugin submits the same shape over inference.upsert.
// Lower is the only path from that shape into the stored Instance, so
// the two producers cannot drift: every field a plugin may declare is a
// field the settings page may edit, and the source policy (row identity,
// credential namespace, the user-owned enabled flag) is enforced once.
//
// Every provider-spec leaf the drivers accept is modeled as a typed
// field on InstanceAdvanced, so there is no opaque provider-spec bag:
// the host is the single place that knows the driver vocabulary, and a
// driver leaf opencraft does not model is a build-time error rather
// than a silently preserved key.

// InstanceSource selects the write policy Lower applies.
type InstanceSource int

const (
	// SourceUser is the desktop settings page: the host assigns row
	// identity when the submitted row has none, and any credential
	// source may be used.
	SourceUser InstanceSource = iota
	// SourcePlugin is a capability plugin: the row carries its own
	// identity, its credential must reference the calling plugin's
	// secret namespace, and the user-owned enabled flag is rejected.
	SourcePlugin
)

// Key-source names on the wire.
const (
	KeySourceEnvName      = "env"
	KeySourceLiteralName  = "literal"
	KeySourceKeychainName = "keychain"
)

// KeySourceName renders one stored key source as its wire name.
func KeySourceName(src KeySource) string {
	switch src {
	case KeyKeychain:
		return KeySourceKeychainName
	case KeyLiteral:
		return KeySourceLiteralName
	default:
		return KeySourceEnvName
	}
}

// InstanceSpec is one inference deployment as submitted by a
// settings-page save or a plugin upsert.
type InstanceSpec struct {
	// StableID is the row identity. A settings-page row may omit it
	// (the host assigns one on the first save); a plugin row must carry
	// it, because plugin upserts are keyed by identity.
	StableID string `json:"stable_id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	// Driver names the flowcraft driver impl for a vendor that is not in
	// the built-in preset catalog; empty uses the preset that Type names.
	Driver   string `json:"driver,omitempty"`
	API      string `json:"api,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	// KeySource is env | literal | keychain. Empty means "unchanged":
	// the settings page omits it when the user did not touch the
	// credential and the stored one is carried over. A plugin row states
	// keychain explicitly (or omits it, which means the same).
	KeySource string `json:"key_source,omitempty"`
	// KeyRef is the credential-store account of a keychain row. The
	// secret itself lives in the credential store, never here.
	KeyRef string `json:"key_ref,omitempty"`
	// KeyValue is a literal key typed into the settings page. It is
	// write-only: reads never echo it back, and plugin rows cannot set
	// it (plugins store the secret in their own namespace instead).
	KeyValue string `json:"key_value,omitempty"`
	// Enabled is user-owned: the settings page states it, while a plugin
	// row is always enabled and must not carry an explicit false.
	Enabled  *bool            `json:"enabled,omitempty"`
	Advanced InstanceAdvanced `json:"advanced"`
	Models   []ModelSpec      `json:"models"`
}

// ModelSpec is one model declaration in an InstanceSpec. Capabilities,
// limits, and lifecycle are the canonical config DTOs, so a plugin
// declaration carries exactly what the settings page edits.
type ModelSpec struct {
	Name         string                  `json:"name"`
	Kind         string                  `json:"kind,omitempty"`
	Capabilities model.ModelCapabilities `json:"capabilities,omitempty"`
	// Endpoint binds this model to a per-model deployment address
	// (ByteDance Ark ep-xxx ids); empty addresses the model by name.
	Endpoint string            `json:"endpoint,omitempty"`
	Limits   model.ModelLimits `json:"limits,omitempty"`
	// Lifecycle is the model's discovery metadata; empty means active.
	Lifecycle ModelLifecycle `json:"lifecycle,omitempty"`
	// DriverFields carries driver-specific model leaves (ByteDance's
	// max_resolution and Seedance parameter matrix, MiniMax's
	// wire_model and video surface). The writer allowlists them per
	// driver.
	DriverFields map[string]any `json:"driver_fields,omitempty"`
}

// stableIDPattern bounds one row identity. It must also be valid as a
// flowcraft provider profile id ([A-Za-z0-9_-] only), so plugin ids
// containing dots are not accepted here even though plugin registry ids
// allow them.
var stableIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidateStableID checks one inference row identity.
func ValidateStableID(id string) error {
	if !stableIDPattern.MatchString(id) {
		return fmt.Errorf("inference: invalid provider instance id %q", id)
	}
	return nil
}

// Lower validates one submitted row and lowers it into the stored
// instance. src selects the write policy; pluginID names the calling
// plugin for SourcePlugin rows (the credential namespace it may use).
func (s InstanceSpec) Lower(src InstanceSource, pluginID string) (Instance, error) {
	in := Instance{
		StableID: strings.TrimSpace(s.StableID),
		Type:     strings.TrimSpace(s.Type),
		Name:     strings.TrimSpace(s.Name),
		Driver:   strings.TrimSpace(s.Driver),
		API:      strings.TrimSpace(s.API),
		Endpoint: strings.TrimSpace(s.Endpoint),
		Advanced: s.Advanced.normalized(),
	}
	if src == SourcePlugin {
		if err := ValidateStableID(in.StableID); err != nil {
			return Instance{}, err
		}
	}
	if in.Type == "" {
		return Instance{}, fmt.Errorf("inference: provider type is required")
	}
	prov, ok := ProviderFor(in)
	if !ok {
		return Instance{}, fmt.Errorf(
			"inference: unknown provider type %q (set driver to declare a "+
				"provider outside the built-in presets)", in.Type,
		)
	}
	if in.Endpoint != "" && !httpURL(in.Endpoint) {
		return Instance{}, fmt.Errorf(
			"inference: invalid endpoint %q", in.Endpoint,
		)
	}
	// responses | chat is an OpenAI-wire surface. Every other driver has
	// a single API, so accepting the field would drop the user's choice
	// without saying so.
	if in.API != "" && !prov.OpenAIWire() {
		return Instance{}, fmt.Errorf(
			"inference: %s does not take an api mode; only the OpenAI wire "+
				"family selects between responses and chat", in.Type,
		)
	}
	if err := lowerCredential(&in, s, src, pluginID, prov); err != nil {
		return Instance{}, err
	}
	if err := in.Advanced.Validate(prov, in.API); err != nil {
		return Instance{}, fmt.Errorf("inference: instance %s: %w", in.Type, err)
	}
	models, err := lowerModels(s.Models)
	if err != nil {
		return Instance{}, err
	}
	in.Models = models

	in.Enabled = true
	if s.Enabled != nil {
		if src == SourcePlugin && !*s.Enabled {
			return Instance{}, fmt.Errorf(
				"inference: enabled is user-owned; a plugin row is always enabled",
			)
		}
		in.Enabled = *s.Enabled
	}
	return in, nil
}

// lowerCredential applies the credential policy of one source. The
// stored KeyValue holds the literal key (KeyLiteral) or the credential
// store account (KeyKeychain).
func lowerCredential(
	in *Instance, s InstanceSpec, src InstanceSource, pluginID string, prov Provider,
) error {
	name := strings.ToLower(strings.TrimSpace(s.KeySource))
	ref := strings.TrimSpace(s.KeyRef)
	value := strings.TrimSpace(s.KeyValue)

	if src == SourcePlugin {
		if value != "" {
			return fmt.Errorf(
				"inference: a plugin row cannot carry a literal key; store " +
					"it in the plugin secret namespace and reference it",
			)
		}
		switch name {
		case "", KeySourceKeychainName:
			if !strings.HasPrefix(ref, "auth/"+pluginID+"/") {
				return fmt.Errorf(
					"inference: key ref %q outside plugin namespace", ref,
				)
			}
			in.KeySource, in.KeyValue = KeyKeychain, ref
			return nil
		default:
			return fmt.Errorf(
				"inference: key source %q is not available to plugins; "+
					"a plugin row references its own secret namespace",
				name,
			)
		}
	}

	switch name {
	case "":
		// Unstated: the settings page did not touch the credential. The
		// caller carries a stored credential over when there is one; a
		// row without any keeps the env source, which is how a keyless
		// (typically disabled) row has always been written.
		if prov.EnvVar == "" {
			return fmt.Errorf(
				"inference: provider type %q needs an explicit key source", in.Type,
			)
		}
		in.KeySource = KeyEnv
		return nil
	case KeySourceEnvName:
		if prov.EnvVar == "" {
			return fmt.Errorf(
				"inference: provider type %q has no environment key source",
				in.Type,
			)
		}
		if os.Getenv(prov.EnvVar) == "" {
			return fmt.Errorf(
				"environment variable %s is not set; cannot use the env key source",
				prov.EnvVar,
			)
		}
		in.KeySource = KeyEnv
		return nil
	case KeySourceLiteralName:
		in.KeySource = KeyLiteral
		in.KeyValue = value
		return nil
	case KeySourceKeychainName:
		if ref == "" {
			return fmt.Errorf("inference: keychain key source needs a key ref")
		}
		in.KeySource, in.KeyValue = KeyKeychain, ref
		return nil
	default:
		return fmt.Errorf("inference: unknown key source %q", name)
	}
}

// requiresKey reports whether one lowered row is enabled but carries no
// usable credential. Env rows reference the variable instead of storing
// a value, so they always pass.
func requiresKey(in Instance) bool {
	if !in.Enabled || in.KeySource == KeyEnv {
		return false
	}
	return strings.TrimSpace(in.KeyValue) == ""
}

// lowerModels lowers and validates the model list of one row.
func lowerModels(specs []ModelSpec) ([]Model, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("inference: a deployment needs at least one model")
	}
	models := make([]Model, 0, len(specs))
	for _, m := range specs {
		models = append(models, m.Lower())
	}
	return models, nil
}

// Lower maps one model declaration onto the stored model. Field
// validation stays with normalizeModels, which the writer runs.
func (m ModelSpec) Lower() Model {
	return Model{
		Name:         strings.TrimSpace(m.Name),
		Kind:         strings.TrimSpace(m.Kind),
		Capabilities: m.Capabilities,
		Endpoint:     strings.TrimSpace(m.Endpoint),
		Limits:       m.Limits,
		Lifecycle:    m.Lifecycle.normalized(),
		DriverFields: m.DriverFields,
	}
}

// InstanceToSpec projects a stored instance onto the wire shape. The
// literal key is never echoed: a keychain row exposes its store account
// as KeyRef, and every other credential source reports key_set instead
// (the settings-page view computes that flag).
func InstanceToSpec(in Instance) InstanceSpec {
	spec := InstanceSpec{
		StableID:  in.StableID,
		Type:      in.Type,
		Name:      in.Name,
		Driver:    in.Driver,
		API:       in.API,
		Endpoint:  in.Endpoint,
		KeySource: KeySourceName(in.KeySource),
		Advanced:  in.Advanced,
		Models:    make([]ModelSpec, 0, len(in.Models)),
	}
	enabled := in.Enabled
	spec.Enabled = &enabled
	if in.KeySource == KeyKeychain {
		spec.KeyRef = in.KeyValue
	}
	for _, m := range in.Models {
		spec.Models = append(spec.Models, ModelToSpec(m))
	}
	return spec
}

// ModelToSpec projects one stored model onto the wire shape.
func ModelToSpec(m Model) ModelSpec {
	// The two structs are field-for-field mirrors; only the JSON tags
	// differ, so a conversion keeps them from drifting.
	return ModelSpec(m)
}

// normalized trims the provider knobs and drops empty map entries, so a
// blank form row never pins a spec leaf.
func (a InstanceAdvanced) normalized() InstanceAdvanced {
	a.Routing = strings.TrimSpace(a.Routing)
	a.Organization = strings.TrimSpace(a.Organization)
	a.Project = strings.TrimSpace(a.Project)
	a.Timeout = strings.TrimSpace(a.Timeout)
	a.Region = strings.TrimSpace(a.Region)
	a.AuthScheme = strings.TrimSpace(a.AuthScheme)
	a.AuthHeader = strings.TrimSpace(a.AuthHeader)
	a.MetadataEnvelope = strings.TrimSpace(a.MetadataEnvelope)
	a.Store = strings.TrimSpace(a.Store)
	a.ReasoningChannel = strings.TrimSpace(a.ReasoningChannel)
	a.ReasoningSummary = strings.TrimSpace(a.ReasoningSummary)
	a.ReasoningScope = strings.TrimSpace(a.ReasoningScope)
	a.Truncation = strings.TrimSpace(a.Truncation)
	a.MediaBaseURL = strings.TrimSpace(a.MediaBaseURL)
	a.Query = trimStringMap(a.Query)
	a.Headers = trimStringMap(a.Headers)
	a.ExtraBody = trimStringMap(a.ExtraBody)
	return a
}

// Validate checks the provider knobs opencraft itself constrains. The
// driver's own strict spec decode stays the final arbiter for values
// opencraft only passes through.
func (a InstanceAdvanced) Validate(prov Provider, api string) error {
	switch a.Store {
	case "", "true", "false", "omit":
	default:
		return fmt.Errorf(
			"store %q must be true, false, or omit", a.Store,
		)
	}
	if a.ChatIncludeUsage != nil || a.ChatIncludeObfuscation != nil {
		if !prov.OpenAIWire() || !strings.EqualFold(api, "chat") {
			return fmt.Errorf(
				"chat stream options require an OpenAI-wire chat deployment",
			)
		}
	}
	if a.VideoPollIntervalMillis != nil {
		if prov.Impl != "bytedance" && prov.Impl != "minimax" {
			return fmt.Errorf(
				"video poll interval is a media deployment knob",
			)
		}
		if *a.VideoPollIntervalMillis <= 0 {
			return fmt.Errorf("video poll interval must be positive")
		}
	}
	return nil
}

// normalized trims the lifecycle fields so a blank form row does not
// write a half-empty block.
func (l ModelLifecycle) normalized() ModelLifecycle {
	return ModelLifecycle{
		Status:              strings.TrimSpace(l.Status),
		ReplacementProvider: strings.TrimSpace(l.ReplacementProvider),
		ReplacementName:     strings.TrimSpace(l.ReplacementName),
		Notes:               strings.TrimSpace(l.Notes),
	}
}

// trimStringMap drops blank entries so an empty form row never pins a
// provider spec key.
func trimStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// httpURL reports whether value is an absolute http(s) URL.
func httpURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// normalizeSpec makes two specs comparable: credential fields are
// masked (the settings page may leave them unstated) and empty slices
// and maps fold to nil (JSON round trips turn them into empty values).
func normalizeSpec(spec InstanceSpec) InstanceSpec {
	spec.KeySource, spec.KeyRef, spec.KeyValue = "", "", ""
	if len(spec.Models) == 0 {
		spec.Models = nil
	}
	if len(spec.Advanced.Query) == 0 {
		spec.Advanced.Query = nil
	}
	if len(spec.Advanced.Headers) == 0 {
		spec.Advanced.Headers = nil
	}
	if len(spec.Advanced.ExtraBody) == 0 {
		spec.Advanced.ExtraBody = nil
	}
	for i := range spec.Models {
		m := &spec.Models[i]
		if len(m.Capabilities.Inputs) == 0 {
			m.Capabilities.Inputs = nil
		}
		if len(m.Capabilities.Outputs) == 0 {
			m.Capabilities.Outputs = nil
		}
		if len(m.Capabilities.Reasoning.EffortMap) == 0 {
			m.Capabilities.Reasoning.EffortMap = nil
		}
		if len(m.DriverFields) == 0 {
			m.DriverFields = nil
		}
	}
	return spec
}

// SpecContentEqual reports whether a submitted row differs from the
// stored one in any field its producer may edit. Credentials are
// excluded: a row that did not restate its credential is unchanged.
func SpecContentEqual(stored Instance, submitted InstanceSpec) bool {
	a := normalizeSpec(InstanceToSpec(stored))
	b := normalizeSpec(submitted)
	b.StableID = a.StableID
	return specDeepEqual(a, b)
}

func specDeepEqual(a, b InstanceSpec) bool {
	if a.StableID != b.StableID || a.Type != b.Type || a.Name != b.Name ||
		a.Driver != b.Driver || a.API != b.API || a.Endpoint != b.Endpoint {
		return false
	}
	// Enabled is user-owned on every row, so it is not part of the
	// content a producer owns.
	if !advancedEqual(a.Advanced, b.Advanced) {
		return false
	}
	if len(a.Models) != len(b.Models) {
		return false
	}
	for i := range a.Models {
		if !modelSpecEqual(a.Models[i], b.Models[i]) {
			return false
		}
	}
	return true
}

func advancedEqual(a, b InstanceAdvanced) bool {
	if a.Routing != b.Routing || a.Organization != b.Organization ||
		a.Project != b.Project || a.Timeout != b.Timeout ||
		a.Region != b.Region || a.AuthScheme != b.AuthScheme ||
		a.AuthHeader != b.AuthHeader ||
		a.MetadataEnvelope != b.MetadataEnvelope || a.Store != b.Store ||
		a.ReasoningChannel != b.ReasoningChannel ||
		a.ReasoningSummary != b.ReasoningSummary ||
		a.ReasoningScope != b.ReasoningScope ||
		a.Truncation != b.Truncation || a.VideoInput != b.VideoInput ||
		a.MediaBaseURL != b.MediaBaseURL {
		return false
	}
	if !boolPtrEqual(a.IncludeReasoningPayload, b.IncludeReasoningPayload) ||
		!boolPtrEqual(a.ChatIncludeUsage, b.ChatIncludeUsage) ||
		!boolPtrEqual(a.ChatIncludeObfuscation, b.ChatIncludeObfuscation) ||
		!ptrEqual(a.HTTPRetries, b.HTTPRetries) ||
		!ptrEqual(a.VideoPollIntervalMillis, b.VideoPollIntervalMillis) {
		return false
	}
	return reflect.DeepEqual(a.Query, b.Query) &&
		reflect.DeepEqual(a.Headers, b.Headers) &&
		reflect.DeepEqual(a.ExtraBody, b.ExtraBody)
}

func modelSpecEqual(a, b ModelSpec) bool {
	if a.Name != b.Name || a.Kind != b.Kind || a.Endpoint != b.Endpoint {
		return false
	}
	if !reflect.DeepEqual(a.Capabilities, b.Capabilities) ||
		!reflect.DeepEqual(a.Limits, b.Limits) {
		return false
	}
	if a.Lifecycle != b.Lifecycle {
		return false
	}
	return reflect.DeepEqual(a.DriverFields, b.DriverFields)
}

// boolPtrEqual compares two optional booleans; nil means "not stated".
func boolPtrEqual(a, b *bool) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

// ptrEqual compares two optional numbers; nil means "not stated".
func ptrEqual[T comparable](a, b *T) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}
