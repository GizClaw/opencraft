package desktop

// Inference profile bindings: turn a plugin-supplied inference profile
// into a configured provider. The host validates the profile (the
// plugin may only manage its own deployment and key namespace), writes
// the provider + router into the user config, rebuilds the runtime and
// notifies the frontend. The host does not interpret the profile's
// domain meaning (gateway, session, ...): that knowledge lives in the
// capability plugin.

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/GizClaw/flowcraft/core/inference"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/plugins"
	pluginruntime "github.com/GizClaw/opencraft/internal/plugins/runtime"
)

// upsertInferenceProfile validates and writes one inference profile
// (no rebuild; the runtime handler rebuilds and notifies).
// pluginID is the calling capability plugin; the profile id and key
// reference must belong to it (a plugin cannot touch another plugin's
// deployment or credentials).
func (a *App) upsertInferenceProfile(pluginID string, profile pluginruntime.InferenceProfile) error {
	if profile.ID != pluginID {
		return fmt.Errorf("inference: profile id %q does not match plugin %q", profile.ID, pluginID)
	}
	if err := plugins.ValidateID(profile.ID); err != nil {
		return fmt.Errorf("inference: %w", err)
	}
	if !strings.HasPrefix(profile.KeyRef, "auth/"+pluginID+"/") {
		return fmt.Errorf("inference: key ref %q outside plugin namespace", profile.KeyRef)
	}
	if len(profile.Models) == 0 {
		return errors.New("inference: profile has no models")
	}
	if profile.Endpoint != "" {
		u, err := url.Parse(profile.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("inference: invalid endpoint %q", profile.Endpoint)
		}
	}
	if _, ok := config.ProviderByID(profile.Type); !ok {
		return fmt.Errorf("inference: unknown provider type %q", profile.Type)
	}

	models := make([]config.Model, 0, len(profile.Models))
	for _, m := range profile.Models {
		if m.Name == "" {
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
	cfg, err := config.LoadInference(a.userDir)
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
	if err := config.WriteInference(a.userDir, cfg); err != nil {
		return err
	}
	return nil
}

// profileModel lowers one plugin profile model into the canonical
// config model. Capabilities are declared as content-kind lists.
func profileModel(m pluginruntime.ProfileModel) config.Model {
	model := config.Model{
		Name:       m.Name,
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

// removeInferenceProfile removes one provider (plugin deployment id)
// from the inference config and rebuilds. Credentials are untouched;
// the plugin clears its own secrets.
func (a *App) removeInferenceProfile(id string) error {
	if err := plugins.ValidateID(id); err != nil {
		return fmt.Errorf("inference: %w", err)
	}
	cfg, err := config.LoadInference(a.userDir)
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
		if err := config.RemoveInferenceConfig(a.userDir); err != nil {
			return err
		}
	} else if err := config.WriteInference(a.userDir, cfg); err != nil {
		return err
	}
	return nil
}
