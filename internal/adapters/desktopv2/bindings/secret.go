package bindings

import (
	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
)

// Secret exposes the scoped credential store to the UI/plugins.
type Secret struct {
	core *core.Core
}

// NewSecretBinding wires the secret binding.
func NewSecretBinding(c *core.Core) *Secret {
	return &Secret{core: c}
}

// Exists reports whether one scoped secret exists.
func (b *Secret) Exists(scope, name string) (bool, error) {
	ctx := b.core.Shell.Context()
	if err := plugins.ValidateSecretRef(scope, name); err != nil {
		return false, err
	}
	_, found, err := b.core.Plugin.Secrets.Get(
		ctx, plugins.SecretAccount(scope, name),
	)
	return found, err
}

// Set stores one scoped secret.
func (b *Secret) Set(scope, name, value string) error {
	ctx := b.core.Shell.Context()
	if err := plugins.ValidateSecretRef(scope, name); err != nil {
		return err
	}
	return b.core.Plugin.Secrets.Set(
		ctx, plugins.SecretAccount(scope, name), value,
	)
}

// Delete removes one scoped secret.
func (b *Secret) Delete(scope, name string) error {
	ctx := b.core.Shell.Context()
	if err := plugins.ValidateSecretRef(scope, name); err != nil {
		return err
	}
	return b.core.Plugin.Secrets.Delete(
		ctx, plugins.SecretAccount(scope, name),
	)
}
