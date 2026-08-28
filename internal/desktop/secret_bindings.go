package desktop

// Phase 1 plugin bindings: a namespaced secret store surface for the
// frontend host. The frontend can only ask whether a secret exists or
// delete it; values are written by Go-side flows only and never cross
// the JS boundary.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// allowedSecretScopes is the closed set of namespaces the frontend may
// query. Unknown scopes fail closed.
var allowedSecretScopes = map[string]bool{
	"inference": true,
	"auth":      true,
}

// secretNameRe allows letters, digits, dot, underscore, dash and
// forward slash (accounts like "auth/sso-haivivi/token"), bounded to
// 128 chars and never starting with a dot.
var secretNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,127}$`)

// SecretExists reports whether a scoped secret exists. The value is
// never returned to the frontend.
func (a *App) SecretExists(scope, name string) (bool, error) {
	if err := validateSecretRef(scope, name); err != nil {
		return false, err
	}
	if a.secrets == nil {
		return false, errors.New("opencraft secrets: store is unavailable")
	}
	_, found, err := a.secrets.Get(a.appContext(), secretAccount(scope, name))
	return found, err
}

// SecretDelete removes one scoped secret (used by logout flows).
func (a *App) SecretDelete(scope, name string) error {
	if err := validateSecretRef(scope, name); err != nil {
		return err
	}
	if a.secrets == nil {
		return errors.New("opencraft secrets: store is unavailable")
	}
	return a.secrets.Delete(a.appContext(), secretAccount(scope, name))
}

func validateSecretRef(scope, name string) error {
	if !allowedSecretScopes[scope] {
		return fmt.Errorf("opencraft secrets: unknown scope %q", scope)
	}
	if strings.TrimSpace(name) == "" || !secretNameRe.MatchString(name) {
		return fmt.Errorf("opencraft secrets: invalid secret name %q", name)
	}
	return nil
}

// secretAccount renders the manager account for a scoped secret. The
// scope prefix keeps plugin secrets out of the inference account
// namespace ("inference/<deployment-id>").
func secretAccount(scope, name string) string {
	return scope + "/" + name
}
