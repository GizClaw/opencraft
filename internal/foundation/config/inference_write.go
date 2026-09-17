package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/GizClaw/opencraft/internal/foundation/compat"
)

// Writing the inference document: renders the user-layer YAML from the
// typed configuration and persists it while holding inferenceStateMu.

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
// endpoint plus every advanced knob the instance sets. The shape follows
// the driver: OpenAI and Anthropic take an endpoint object, ByteDance
// takes flat transport fields, and MiniMax names its media origin
// separately. Every leaf the drivers accept is modeled on
// InstanceAdvanced, so this writer is the whole provider spec.
func writeProviderSpec(
	b *strings.Builder, prov Provider, in Instance, apiMode string,
) error {
	adv := in.Advanced
	if apiMode != "" && prov.OpenAIWire() {
		fmt.Fprintf(b, "        api: %s\n", yamlQuote(apiMode))
	}
	baseURL := strings.TrimSpace(in.Endpoint)
	switch prov.Impl {
	case "bytedance":
		if baseURL != "" {
			fmt.Fprintf(b, "        base_url: %s\n", yamlQuote(baseURL))
		}
		for _, key := range []struct {
			name  string
			value string
		}{
			{"region", adv.Region},
			{"project", adv.Project},
			{"timeout", adv.Timeout},
			{"reasoning_scope", adv.ReasoningScope},
		} {
			if key.value == "" {
				continue
			}
			fmt.Fprintf(b, "        %s: %s\n", key.name, yamlQuote(key.value))
		}
		writeStringMap(b, "        ", "headers", adv.Headers)
		writeStringMap(b, "        ", "query", adv.Query)
	case "minimax":
		if baseURL != "" {
			fmt.Fprintf(b, "        media_base_url: %s\n", yamlQuote(baseURL))
		}
	default:
		// OpenAI wire family and Anthropic: one endpoint object.
		routing := strings.TrimSpace(adv.Routing)
		endpointKeys := baseURL != "" || routing != "" || adv.Organization != "" ||
			adv.Project != "" || adv.Timeout != "" ||
			len(adv.Query) > 0 || len(adv.Headers) > 0
		if endpointKeys {
			fmt.Fprintf(b, "        endpoint:\n")
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
			writeStringMap(b, "          ", "headers", adv.Headers)
			writeStringMap(b, "          ", "query", adv.Query)
		}
		scheme := strings.TrimSpace(adv.AuthScheme)
		if scheme != "" {
			fmt.Fprintf(b, "        auth:\n")
			fmt.Fprintf(b, "          scheme: %s\n", yamlQuote(scheme))
			header := strings.TrimSpace(adv.AuthHeader)
			if header != "" {
				fmt.Fprintf(b, "          header: %s\n", yamlQuote(header))
			}
		}
	}
	// Wire dialect.
	channel := strings.TrimSpace(adv.ReasoningChannel)
	scope := strings.TrimSpace(adv.ReasoningScope)
	switch prov.Impl {
	case "openai":
		wireKeys := channel != "" || adv.VideoInput ||
			adv.Store != "" || scope != "" ||
			adv.IncludeReasoningPayload != nil || adv.ReasoningSummary != "" ||
			adv.Truncation != "" || adv.ChatIncludeUsage != nil ||
			adv.ChatIncludeObfuscation != nil
		if wireKeys {
			fmt.Fprintf(b, "        wire:\n")
			if scope != "" {
				fmt.Fprintf(b, "          reasoning_scope: %s\n", yamlQuote(scope))
			}
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
						return fmt.Errorf(
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
		if adv.VideoInput || scope != "" {
			fmt.Fprintf(b, "        wire:\n")
			if scope != "" {
				fmt.Fprintf(b, "          reasoning_scope: %s\n", yamlQuote(scope))
			}
			if adv.VideoInput {
				fmt.Fprintf(b, "          video_input: true\n")
			}
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
	}
	if adv.HTTPRetries != nil {
		fmt.Fprintf(b, "        http_retries: %d\n", *adv.HTTPRetries)
	}
	if prov.Impl == "bytedance" && adv.VideoPollIntervalMillis != nil {
		fmt.Fprintf(b, "        video_poll_interval_millis: %d\n",
			*adv.VideoPollIntervalMillis)
	}
	return nil
}

// writeStringMap renders one string map at the given indentation,
// skipping the block when it has no entries.
func writeStringMap(
	b *strings.Builder,
	indent, key string,
	values map[string]string,
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
		// A composite value nests one level under its key; a scalar stays
		// inline. The decision cannot come from the marshaled line count:
		// a single-entry map marshals to one line, and inlining it would
		// emit "key: inner: value", which is not a YAML mapping at all.
		if !isCompositeDriverField(ordered[key]) {
			fmt.Fprintf(b, "            %s: %s\n", key, lines[0])
			continue
		}
		fmt.Fprintf(b, "            %s:\n", key)
		for _, line := range lines {
			fmt.Fprintf(b, "              %s\n", line)
		}
	}
	return nil
}

// isCompositeDriverField reports whether one driver-field value is a
// container (a map or a sequence) rather than a scalar. Values arrive
// decoded from JSON, so containers are map[string]any and []any.
func isCompositeDriverField(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
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

// writeInferenceLocked writes the user inference YAML. Callers hold
// inferenceStateMu.
func writeInferenceLocked(configDir string, cfg InferenceConfig) error {
	fresh, err := cfg.InferenceYAML()
	if err != nil {
		return err
	}
	replaceKeys := managedResourceKeys()
	// Tool knobs are keyed by deployment id, so a provider this write
	// removes would leave its blocks behind as dead configuration — and
	// a later provider re-using the id would silently inherit them. Prune
	// them in the same write, and only then take over those two
	// resources: without a prune the layer stays exactly as the user left
	// it.
	stored, err := LoadToolOptions(configDir)
	if err != nil {
		return err
	}
	if pruned, dropped := PruneToolOptions(stored, cfg.Instances); dropped {
		fresh, err = withToolOptions(fresh, pruned)
		if err != nil {
			return err
		}
		for _, key := range toolResourceKeys {
			replaceKeys[key] = true
		}
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		replaceKeys,
		map[string]bool{},
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
// empty document. The returned flag reports whether the write reached
// disk: producers that re-submit an unchanged row use it to skip the
// follow-up work (a runtime rebuild) that a no-op write cannot justify.
func UpdateInferenceState(
	configDir string,
	update func(
		cfg InferenceConfig,
		owners map[string]string,
	) (InferenceConfig, map[string]string, bool, error),
) (bool, error) {
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	cfg, err := LoadInference(configDir)
	if err != nil {
		return false, err
	}
	owners, err := loadProviderOwnersLocked(configDir)
	if err != nil {
		return false, err
	}
	adoptLegacyProviderOwners(cfg, owners)
	nextCfg, nextOwners, changed, err := update(cfg, owners)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if len(nextCfg.Instances) == 0 {
		if err := removeInferenceConfigLocked(configDir); err != nil {
			return true, err
		}
		return true, saveProviderOwnersLocked(configDir, map[string]string{})
	}
	if err := writeInferenceLocked(configDir, nextCfg); err != nil {
		return true, err
	}
	if nextOwners == nil {
		nextOwners = owners
	}
	return true, saveProviderOwnersLocked(
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
	if _, err := UpdateInferenceState(configDir, func(
		cfg InferenceConfig,
		owners map[string]string,
	) (InferenceConfig, map[string]string, bool, error) {
		return cfg, owners, true, nil
	}); err != nil {
		return false, fmt.Errorf("config: migrate user inference document: %w", err)
	}
	return true, nil
}

// managedResourceKeys returns the user-layer resources WriteInference
// replaces wholesale: the router policy and the infer dep wiring.
// Provider.* resources are managed separately by dropping every old
// provider key and keeping only the freshly generated ones.
func managedResourceKeys() map[string]bool {
	return map[string]bool{"router": true, "infer": true}
}
