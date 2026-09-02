package toolchain

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
)

// Factory builds the runtimes resource from deploy settings. The
// value is a *Manager consumed by the sandbox factory, MCP source
// factories and diagnostics.
type Factory struct{}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ResourceKind,
		Impl: ResourceImpl,
	}
}

// New implements resource.Factory.
func (Factory) New(ctx context.Context, in resource.Input) (any, error) {
	settings := DefaultSettings()
	if len(in.Settings) > 0 {
		decoded, err := resource.DecodeTyped[Settings](ctx, in.Settings)
		if err != nil {
			return nil, errdefs.Validationf(
				"opencraft runtimes: decode settings: %v", err)
		}
		settings = decoded
	}
	pref, err := NormalizePreference(settings.Preference)
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft runtimes: %v", err)
	}
	m, err := New(Options{
		Preference:      pref,
		Root:            settings.Root,
		ManifestPath:    settings.ManifestPath,
		SandboxCacheDir: settings.SandboxCacheDir,
		HostCacheDir:    settings.HostCacheDir,
	})
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft runtimes: %v", err)
	}
	return m, nil
}

// Register adds the runtimes resource factory to r.
func Register(r *resource.Registry) error {
	return r.Register(Factory{})
}
