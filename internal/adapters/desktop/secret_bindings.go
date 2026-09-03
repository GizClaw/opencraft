package desktop

// Namespaced secret store bindings: the frontend can only ask whether
// a secret exists or delete it; values are written by Go-side flows
// only and never cross the JS boundary.

import (
	"errors"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
)

// SecretExists reports whether a scoped secret exists.
func (a *App) SecretExists(scope, name string) (bool, error) {
	if err := plugins.ValidateSecretRef(scope, name); err != nil {
		return false, err
	}
	if a.secrets == nil {
		return false, errors.New("opencraft secrets: store is unavailable")
	}
	_, found, err := a.secrets.Get(a.appContext(), plugins.SecretAccount(scope, name))
	return found, err
}

// SecretDelete removes one scoped secret (used by logout flows).
func (a *App) SecretDelete(scope, name string) error {
	if err := plugins.ValidateSecretRef(scope, name); err != nil {
		return err
	}
	if a.secrets == nil {
		return errors.New("opencraft secrets: store is unavailable")
	}
	return a.secrets.Delete(a.appContext(), plugins.SecretAccount(scope, name))
}
