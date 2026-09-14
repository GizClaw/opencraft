package config

import (
	"errors"
	"fmt"
	"strings"
)

// The write API used by both producers of inference rows. Every rule
// about which row a source may write, how a submitted row becomes a
// stored one, and which stored rows survive a settings save lives here;
// the desktop bindings and the plugin runtime only map their DTOs and
// call in (see inference_spec.go for the shared row shape).

// SaveRequest is one settings-page save: the full instance list in
// router priority order plus the retry policy.
type SaveRequest struct {
	Instances []InstanceSpec
	Router    RouterPolicy
}

// ApplySettingsSave replaces the inference configuration with one
// settings-page save. Plugin-managed rows keep their stored content
// (order and priority stay user-controlled) and rows the page no longer
// submits stay declared; their ids are returned so the page can tell the
// user why an edit did not stick. isManaged answers whether a row
// belongs to an installed, enabled plugin; nil means "none".
func ApplySettingsSave(
	configDir string, req SaveRequest, isManaged func(stableID string) bool,
) (restored []string, err error) {
	if isManaged == nil {
		isManaged = func(string) bool { return false }
	}
	next := InferenceConfig{}
	err = UpdateInferenceState(configDir, func(
		existing InferenceConfig,
		owners map[string]string,
	) (InferenceConfig, map[string]string, bool, error) {
		stored := make(map[string]Instance, len(existing.Instances))
		for _, in := range existing.Instances {
			if in.StableID != "" {
				stored[in.StableID] = in
			}
		}
		instances := make([]Instance, 0, len(req.Instances)+len(existing.Instances))
		seen := make(map[string]bool, len(req.Instances))
		for _, spec := range req.Instances {
			if spec.StableID != "" && isManaged(spec.StableID) {
				if in, ok := stored[spec.StableID]; ok {
					// Content is the plugin's; the enabled toggle is the
					// user's, so it is applied on top of the stored row.
					if spec.Enabled != nil {
						in.Enabled = *spec.Enabled
					}
					if !SpecContentEqual(stored[spec.StableID], spec) {
						restored = append(restored, in.StableID)
					}
					instances = append(instances, in)
					seen[in.StableID] = true
					continue
				}
			}
			carried := carryCredential(spec, stored)
			if !carried.statesCredential() &&
				(carried.Enabled == nil || *carried.Enabled) {
				// An enabled row with nothing typed and nothing stored
				// cannot be addressed: the page asks for a key instead
				// of silently pinning the provider's env variable.
				return existing, nil, false, fmt.Errorf(
					"instance %s (%s): an API key or the env key source is required",
					carried.Name, carried.Type,
				)
			}
			in, err := carried.Lower(SourceUser, "")
			if err != nil {
				return existing, nil, false, err
			}
			if in.StableID == "" {
				in.StableID = NewStableID()
			}
			if requiresKey(in) {
				return existing, nil, false, fmt.Errorf(
					"instance %s (%s): an API key or the env key source is required",
					instanceLabel(in), in.Type,
				)
			}
			instances = append(instances, in)
			seen[in.StableID] = true
		}
		// Plugin-managed rows the page did not submit stay declared so a
		// plugin deployment never disappears because of a settings save.
		for _, in := range existing.Instances {
			if seen[in.StableID] || !isManaged(in.StableID) {
				continue
			}
			restored = append(restored, in.StableID)
			instances = append(instances, in)
			seen[in.StableID] = true
		}
		next = InferenceConfig{Instances: instances, Router: req.Router}
		if len(next.Enabled()) == 0 {
			return existing, nil, false, errors.New("enable at least one instance")
		}
		return next, owners, true, nil
	})
	if err != nil {
		return nil, err
	}
	return restored, nil
}

// UpsertPluginInstance writes (or replaces) one plugin-owned inference
// deployment. The row must carry its own identity, and it can only
// replace a row the same plugin owns; the ownership sidecar records the
// claim.
func UpsertPluginInstance(configDir, pluginID string, spec InstanceSpec) error {
	in, err := spec.Lower(SourcePlugin, pluginID)
	if err != nil {
		return err
	}
	return UpdateInferenceState(configDir, func(
		cfg InferenceConfig,
		owners map[string]string,
	) (InferenceConfig, map[string]string, bool, error) {
		for i := range cfg.Instances {
			if cfg.Instances[i].StableID != in.StableID {
				continue
			}
			if !inferenceOwnedBy(owners, in.StableID, pluginID) {
				return cfg, nil, false, fmt.Errorf(
					"inference: provider instance %q is not owned by plugin %q",
					in.StableID, pluginID,
				)
			}
			// The plugin owns the deployment; the user owns whether it
			// is routed, so a re-upsert keeps the stored toggle.
			in.Enabled = cfg.Instances[i].Enabled
			cfg.Instances[i] = in
			owners[in.StableID] = pluginID
			return cfg, owners, true, nil
		}
		if owner, ok := owners[in.StableID]; ok && owner != pluginID {
			return cfg, nil, false, fmt.Errorf(
				"inference: provider instance %q is owned by plugin %q",
				in.StableID, owner,
			)
		}
		cfg.Instances = append(cfg.Instances, in)
		owners[in.StableID] = pluginID
		return cfg, owners, true, nil
	})
}

// RemovePluginInstance drops one plugin-owned deployment. Credentials
// are untouched: the plugin clears its own secrets.
func RemovePluginInstance(configDir, pluginID, stableID string) error {
	if err := ValidateStableID(stableID); err != nil {
		return err
	}
	return UpdateInferenceState(configDir, func(
		cfg InferenceConfig,
		owners map[string]string,
	) (InferenceConfig, map[string]string, bool, error) {
		found := false
		for _, in := range cfg.Instances {
			if in.StableID == stableID {
				found = true
				break
			}
		}
		if !found {
			return cfg, owners, false, nil
		}
		if !inferenceOwnedBy(owners, stableID, pluginID) {
			return cfg, nil, false, fmt.Errorf(
				"inference: provider instance %q is not owned by plugin %q",
				stableID, pluginID,
			)
		}
		out := cfg.Instances[:0]
		for _, in := range cfg.Instances {
			if in.StableID == stableID {
				continue
			}
			out = append(out, in)
		}
		cfg.Instances = out
		delete(owners, stableID)
		return cfg, owners, true, nil
	})
}

// RemovePluginInstances removes every inference deployment owned by
// pluginID and reports whether any deployment was removed. It is the
// host-side fallback used when a plugin is disabled or uninstalled;
// secrets are not touched here.
func RemovePluginInstances(configDir, pluginID string) (bool, error) {
	removed := false
	err := UpdateInferenceState(configDir, func(
		cfg InferenceConfig,
		owners map[string]string,
	) (InferenceConfig, map[string]string, bool, error) {
		out := cfg.Instances[:0]
		for _, in := range cfg.Instances {
			if inferenceOwnedBy(owners, in.StableID, pluginID) {
				delete(owners, in.StableID)
				removed = true
				continue
			}
			out = append(out, in)
		}
		cfg.Instances = out
		return cfg, owners, removed, nil
	})
	if err != nil {
		return false, err
	}
	if removed {
		return true, nil
	}
	dropped, err := DropProviderOwners(configDir, pluginID)
	if err != nil {
		return false, err
	}
	return dropped > 0, nil
}

// carryCredential fills in the credential a settings-page row did not
// restate, so editing a row (renaming it, changing a model) never
// destroys its stored key. Rows are matched by stable identity, which
// every saved row carries.
func carryCredential(spec InstanceSpec, stored map[string]Instance) InstanceSpec {
	in, ok := stored[strings.TrimSpace(spec.StableID)]
	if !ok {
		return spec
	}
	if !spec.statesCredential() {
		spec.KeySource = KeySourceName(in.KeySource)
		switch in.KeySource {
		case KeyKeychain:
			spec.KeyRef = in.KeyValue
		case KeyLiteral:
			spec.KeyValue = in.KeyValue
		}
		return spec
	}
	switch {
	case strings.EqualFold(spec.KeySource, KeySourceLiteralName) &&
		spec.KeyValue == "" && in.KeySource == KeyLiteral:
		spec.KeyValue = in.KeyValue
	case strings.EqualFold(spec.KeySource, KeySourceKeychainName) &&
		spec.KeyRef == "" && in.KeySource == KeyKeychain:
		spec.KeyRef = in.KeyValue
	}
	return spec
}

// statesCredential reports whether a submitted row states any part of
// its credential.
func (s InstanceSpec) statesCredential() bool {
	return strings.TrimSpace(s.KeySource) != "" ||
		strings.TrimSpace(s.KeyRef) != "" ||
		strings.TrimSpace(s.KeyValue) != ""
}

// instanceLabel names one row for error messages.
func instanceLabel(in Instance) string {
	if in.Name != "" {
		return in.Name
	}
	return in.Type
}
