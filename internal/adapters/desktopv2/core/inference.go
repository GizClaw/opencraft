package core

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/GizClaw/flowcraft/core/inference"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// wirePluginInference routes capability-plugin inference profile
// primitives into the user config. Plugin-owned deployments stay
// addressable by plugin id, which is what ConfigState reports as
// Managed.
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
		Remove: func(_, id string) error {
			if err := c.removeInferenceProfile(id); err != nil {
				return err
			}
			c.Shell.Emit("inference_changed", map[string]any{})
			return c.ReloadRuntime(c.Shell.Context())
		},
	})
}

// upsertInferenceProfile validates and writes one plugin-owned
// inference deployment. The profile id and key reference must belong
// to the calling plugin.
func (c *Core) upsertInferenceProfile(
	pluginID string, profile pluginruntime.InferenceProfile,
) error {
	if profile.ID != pluginID {
		return fmt.Errorf(
			"inference: profile id %q does not match plugin %q",
			profile.ID, pluginID,
		)
	}
	if err := plugins.ValidateID(profile.ID); err != nil {
		return fmt.Errorf("inference: %w", err)
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
		StableID:  profile.ID,
		Type:      profile.Type,
		Name:      profile.Name,
		API:       profile.API,
		Endpoint:  profile.Endpoint,
		Models:    models,
		KeySource: config.KeyKeychain,
		KeyValue:  profile.KeyRef,
		Enabled:   true,
	}
	cfg, err := config.LoadInference(c.UserDir)
	if err != nil {
		return err
	}
	replaced := false
	for i := range cfg.Instances {
		if cfg.Instances[i].StableID == profile.ID {
			cfg.Instances[i] = inst
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.Instances = append(cfg.Instances, inst)
	}
	if len(cfg.Enabled()) == 0 {
		return errors.New("inference: profile must enable at least one model")
	}
	return config.WriteInference(c.UserDir, cfg)
}

// removeInferenceProfile drops one plugin-owned deployment. Credentials
// are untouched; the plugin clears its own secrets.
func (c *Core) removeInferenceProfile(id string) error {
	if err := plugins.ValidateID(id); err != nil {
		return fmt.Errorf("inference: %w", err)
	}
	cfg, err := config.LoadInference(c.UserDir)
	if err != nil {
		return err
	}
	out := cfg.Instances[:0]
	removed := false
	for _, in := range cfg.Instances {
		if in.StableID == id {
			removed = true
			continue
		}
		out = append(out, in)
	}
	if !removed {
		return nil
	}
	cfg.Instances = out
	if len(cfg.Instances) == 0 {
		return config.RemoveInferenceConfig(c.UserDir)
	}
	return config.WriteInference(c.UserDir, cfg)
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
