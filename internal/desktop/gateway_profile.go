package desktop

// Gateway profile bindings: turn a completed SSO session into a
// configured inference provider. The session metadata is read from the
// secret store (written by the capability plugin), never from the
// frontend; the provider points at the gateway base URL with the
// auth-scoped token reference, so the plaintext never enters the config.

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/plugins"
)

// UpsertGatewayProfile writes (or replaces) the gateway provider for
// providerID (the SSO plugin id, e.g. "sso-haivivi") from the stored
// session meta, then rebuilds the runtime. The plugin does not need to
// pass models or endpoints.
func (a *App) UpsertGatewayProfile(providerID, displayName string) error {
	if err := a.upsertGatewayProfile(providerID, displayName); err != nil {
		return err
	}
	return a.rebuild()
}

// RemoveGatewayProfile removes the gateway provider from the inference
// config and rebuilds. Credentials are untouched (AuthRevoke handles
// them).
func (a *App) RemoveGatewayProfile(providerID string) error {
	if err := a.removeGatewayProfile(providerID); err != nil {
		return err
	}
	return a.rebuild()
}

// upsertGatewayProfile is the testable write path without rebuild.
func (a *App) upsertGatewayProfile(providerID, displayName string) error {
	if err := plugins.ValidateID(providerID); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if a.secrets == nil {
		return errors.New("opencraft secrets: store is unavailable")
	}
	raw, found, err := a.secrets.Get(
		a.appContext(), plugins.SecretAccount("auth", providerID+"/meta"))
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("auth: provider %q is not authenticated", providerID)
	}
	var meta struct {
		BaseURL      string   `json:"base_url"`
		DefaultModel string   `json:"default_model"`
		Models       []string `json:"models"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return fmt.Errorf("auth: decode session meta: %w", err)
	}
	if len(meta.Models) == 0 {
		return fmt.Errorf("auth: provider %q has no models", providerID)
	}
	if displayName == "" {
		displayName = meta.ClientName
	}
	if displayName == "" {
		displayName = "SSO Gateway"
	}
	baseURL := meta.BaseURL
	if baseURL == "" {
		return fmt.Errorf("auth: provider %q meta has no base_url", providerID)
	}
	models := make([]config.Model, 0, len(meta.Models))
	for _, name := range meta.Models {
		models = append(models, config.Model{Name: name})
	}
	inst := config.Instance{
		StableID:  providerID,
		Type:      "openai",
		Name:      displayName,
		API:       "responses",
		Endpoint:  baseURL,
		Models:    models,
		KeySource: config.KeyKeychain,
		KeyValue:  plugins.TokenAccount(providerID),
		Enabled:   true,
	}
	cfg, err := config.LoadInference(a.userDir)
	if err != nil {
		return err
	}
	replaced := false
	for i := range cfg.Instances {
		if cfg.Instances[i].StableID == providerID {
			cfg.Instances[i] = inst
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.Instances = append(cfg.Instances, inst)
	}
	if len(cfg.Enabled()) == 0 {
		return errors.New("gateway profile must enable at least one model")
	}
	return config.WriteInference(a.userDir, cfg)
}

func (a *App) removeGatewayProfile(providerID string) error {
	if err := plugins.ValidateID(providerID); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	cfg, err := config.LoadInference(a.userDir)
	if err != nil {
		return err
	}
	out := cfg.Instances[:0]
	removed := false
	for _, in := range cfg.Instances {
		if in.StableID == providerID {
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
		// Removing the last provider returns the install to the
		// unconfigured state; WriteInference requires one enabled
		// instance, so clear the inference layer explicitly.
		return config.RemoveInferenceConfig(a.userDir)
	}
	return config.WriteInference(a.userDir, cfg)
}
