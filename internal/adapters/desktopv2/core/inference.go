package core

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/GizClaw/flowcraft/core/inference"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// providerStableIDRe bounds a plugin-submitted provider instance id.
// It must also be valid as a flowcraft provider profile id
// ([A-Za-z0-9_-] only), so plugin ids containing dots are not accepted
// here even though plugin registry ids allow them.
var providerStableIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// wirePluginInference routes capability-plugin inference profile
// primitives into the user config. Each submitted instance gets an
// explicit ownership record (config.WriteInferenceOwned sidecar), so a
// plugin may manage several deployments and ConfigState can report
// each one as Managed independently.
func (c *Core) wirePluginInference() {
	if c.Plugin == nil || c.Plugin.Capability == nil {
		return
	}
	c.Plugin.Capability.SetInferenceHandler(pluginruntime.InferenceHandler{
		Upsert: func(pluginID string, profile pluginruntime.InferenceProfile) error {
			if err := c.upsertInferenceProfile(pluginID, profile); err != nil {
				return err
			}
			c.Shell.Emit("inference_changed", map[string]any{})
			return c.ReloadRuntime(c.Shell.Context())
		},
		Remove: func(pluginID, id string) error {
			if err := c.removeInferenceProfile(pluginID, id); err != nil {
				return err
			}
			c.Shell.Emit("inference_changed", map[string]any{})
			return c.ReloadRuntime(c.Shell.Context())
		},
	})
}

func validateProviderStableID(id string) error {
	if !providerStableIDRe.MatchString(id) {
		return fmt.Errorf("inference: invalid provider instance id %q", id)
	}
	return nil
}

// inferenceOwnedBy reports whether stableID is owned by pluginID. The
// explicit sidecar is the only ownership source; instance ids are not
// tied to plugin ids.
func inferenceOwnedBy(
	owners map[string]string,
	stableID string,
	pluginID string,
) bool {
	owner, ok := owners[stableID]
	return ok && owner == pluginID
}

// upsertInferenceProfile validates and writes one plugin-owned
// inference deployment. The key reference must stay inside the
// calling plugin's secret namespace; the instance id is validated and
// then recorded with its owner in the config ownership sidecar.
func (c *Core) upsertInferenceProfile(
	pluginID string, profile pluginruntime.InferenceProfile,
) error {
	if err := validateProviderStableID(profile.ID); err != nil {
		return err
	}
	if !strings.HasPrefix(profile.KeyRef, "auth/"+pluginID+"/") {
		return fmt.Errorf(
			"inference: key ref %q outside plugin namespace", profile.KeyRef,
		)
	}
	if len(profile.Models) == 0 {
		return errors.New("inference: profile has no models")
	}
	if profile.Endpoint != "" {
		u, err := url.Parse(profile.Endpoint)
		if err != nil ||
			(u.Scheme != "http" && u.Scheme != "https") ||
			u.Host == "" {
			return fmt.Errorf(
				"inference: invalid endpoint %q", profile.Endpoint,
			)
		}
	}
	if _, ok := config.ProviderByID(profile.Type); !ok {
		return fmt.Errorf("inference: unknown provider type %q", profile.Type)
	}
	if err := config.ValidateProviderSpec(
		profile.Type, profile.API, profile.ProviderSpec,
	); err != nil {
		return err
	}

	models := make([]config.Model, 0, len(profile.Models))
	for _, m := range profile.Models {
		if strings.TrimSpace(m.Name) == "" {
			continue
		}
		models = append(models, profileModel(m))
	}
	if len(models) == 0 {
		return errors.New("inference: profile has no named models")
	}
	inst := config.Instance{
		StableID:     profile.ID,
		Type:         profile.Type,
		Name:         profile.Name,
		API:          profile.API,
		Endpoint:     profile.Endpoint,
		Models:       models,
		KeySource:    config.KeyKeychain,
		KeyValue:     profile.KeyRef,
		Enabled:      true,
		ProviderSpec: profile.ProviderSpec,
	}
	return config.UpdateInferenceState(
		c.UserDir,
		func(cfg config.InferenceConfig, owners map[string]string) (
			config.InferenceConfig,
			map[string]string,
			bool,
			error,
		) {
			replaced := false
			for i := range cfg.Instances {
				if cfg.Instances[i].StableID == profile.ID {
					if !inferenceOwnedBy(owners, profile.ID, pluginID) {
						return cfg, owners, false, fmt.Errorf(
							"inference: provider instance %q is not owned by plugin %q",
							profile.ID, pluginID,
						)
					}
					cfg.Instances[i] = inst
					replaced = true
					break
				}
			}
			if owner, ok := owners[profile.ID]; ok && owner != pluginID {
				return cfg, owners, false, fmt.Errorf(
					"inference: provider instance %q is owned by plugin %q",
					profile.ID, owner,
				)
			}
			if !replaced {
				cfg.Instances = append(cfg.Instances, inst)
			}
			owners[profile.ID] = pluginID
			return cfg, owners, true, nil
		},
	)
}

// removeInferenceProfile drops one plugin-owned deployment. Credentials
// are untouched; the plugin clears its own secrets.
func (c *Core) removeInferenceProfile(pluginID, id string) error {
	if err := validateProviderStableID(id); err != nil {
		return err
	}
	return config.UpdateInferenceState(
		c.UserDir,
		func(cfg config.InferenceConfig, owners map[string]string) (
			config.InferenceConfig,
			map[string]string,
			bool,
			error,
		) {
			found := false
			for _, in := range cfg.Instances {
				if in.StableID == id {
					found = true
					break
				}
			}
			if !found {
				return cfg, owners, false, nil
			}
			if !inferenceOwnedBy(owners, id, pluginID) {
				return cfg, owners, false, fmt.Errorf(
					"inference: provider instance %q is not owned by plugin %q",
					id, pluginID,
				)
			}
			out := cfg.Instances[:0]
			for _, in := range cfg.Instances {
				if in.StableID == id {
					continue
				}
				out = append(out, in)
			}
			delete(owners, id)
			cfg.Instances = out
			return cfg, owners, true, nil
		},
	)
}

// RemovePluginInference removes every inference deployment owned by
// pluginID and reports whether any deployment was removed. It is the
// host-side fallback used when a plugin is disabled or uninstalled;
// secrets are not touched here.
func (c *Core) RemovePluginInference(pluginID string) (bool, error) {
	if err := plugins.ValidateID(pluginID); err != nil {
		return false, err
	}
	removed := false
	err := config.UpdateInferenceState(
		c.UserDir,
		func(cfg config.InferenceConfig, owners map[string]string) (
			config.InferenceConfig,
			map[string]string,
			bool,
			error,
		) {
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
		},
	)
	if err != nil {
		return false, err
	}
	if removed {
		return true, nil
	}
	dropped, err := config.DropProviderOwners(c.UserDir, pluginID)
	if err != nil {
		return false, err
	}
	return dropped > 0, nil
}

// profileModel lowers one plugin profile model into the canonical
// config model. Capabilities are declared as content-kind lists.
func profileModel(m pluginruntime.ProfileModel) config.Model {
	model := config.Model{
		Name:       strings.TrimSpace(m.Name),
		Endpoint:   strings.TrimSpace(m.Endpoint),
		EffortNone: m.EffortNone,
	}
	caps := inference.ModelCapabilities{
		Reasoning: inference.ReasoningCapability{
			Kind:      inference.ReasoningKind(strings.TrimSpace(m.Reasoning)),
			EffortMap: config.EffortMapEfforts(m.ReasoningEffortMap),
		},
		HostedWebSearch: m.WebSearch,
	}
	if len(m.Inputs) > 0 || len(m.Outputs) > 0 {
		caps.Inputs = config.ToPartKinds(m.Inputs)
		caps.Outputs = config.ToPartKinds(m.Outputs)
	}
	model.Capabilities = caps
	return model
}
