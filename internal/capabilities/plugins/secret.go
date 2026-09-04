package plugins

import (
	"fmt"
	"regexp"
	"strings"
)

// AllowedSecretScopes is the closed set of namespaces the frontend may
// query. Unknown scopes fail closed.
var AllowedSecretScopes = map[string]bool{
	"inference": true,
	"auth":      true,
}

// secretNameRe allows letters, digits, dot, underscore, dash and
// forward slash (accounts like "auth/sso-haivivi/token"), bounded to
// 128 chars and never starting with a dot.
var secretNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,127}$`)

// ValidateSecretRef checks a scoped secret reference.
func ValidateSecretRef(scope, name string) error {
	if !AllowedSecretScopes[scope] {
		return fmt.Errorf("opencraft secrets: unknown scope %q", scope)
	}
	if strings.TrimSpace(name) == "" || !secretNameRe.MatchString(name) {
		return fmt.Errorf("opencraft secrets: invalid secret name %q", name)
	}
	return nil
}

// SecretAccount renders the manager account for a scoped secret. The
// scope prefix keeps plugin secrets out of the inference account
// namespace ("inference/<deployment-id>").
func SecretAccount(scope, name string) string {
	return scope + "/" + name
}
