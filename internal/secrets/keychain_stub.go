//go:build !darwin

package secrets

import (
	"context"
	"errors"
)

// keychainBackend is only constructible on darwin (see
// keychain_darwin.go); the stub keeps the type resolvable on other
// platforms where NewStore never selects it.
type keychainBackend struct{ service string }

func (k *keychainBackend) Available() bool { return false }

func (k *keychainBackend) Get(context.Context, string) (string, bool, error) {
	return "", false, errors.New("opencraft secrets: keychain backend is darwin-only")
}

func (k *keychainBackend) Set(context.Context, string, string) error {
	return errors.New("opencraft secrets: keychain backend is darwin-only")
}

func (k *keychainBackend) Delete(context.Context, string) error {
	return errors.New("opencraft secrets: keychain backend is darwin-only")
}
