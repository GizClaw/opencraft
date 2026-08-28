package desktop

// Gateway profile bindings: turn a completed auth session into a
// configured inference provider. The provider is openai-compatible and
// points at the gateway base URL with the auth-scoped token reference,
// so the plaintext never enters the config.

import (
	"errors"
	"fmt"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/plugins"
)

// UpsertGatewayProfile writes (or replaces) the gateway provider for
// providerID from the stored auth meta, then rebuilds the runtime. The
// provider identity is the auth provider id (e.g. "haivivi"), so the
// plugin does not need to pass models or endpoints.
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
	svc, err := a.authService()
	if err != nil {
		return err
	}
	meta, ok, err := svc.ReadMeta(providerID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("auth: provider %q is not authenticated", providerID)
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
		baseURL = plugins.DefaultGatewayBaseURL + "/v1"
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
