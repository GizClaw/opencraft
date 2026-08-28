package desktop

// Thin wails bindings over plugins.AuthService (Phase 2 device
// authorization primitives). device_code and tokens stay in the
// plugins service / SecretStore; only status and public metadata cross
// the JS boundary.

import (
	"errors"

	"github.com/GizClaw/opencraft/internal/plugins"
)

func (a *App) authService() (*plugins.AuthService, error) {
	if a.auth == nil {
		return nil, errors.New("auth service is not ready")
	}
	return a.auth, nil
}

// AuthBegin creates a device authorization and opens the system
// browser at the confirmation URL.
func (a *App) AuthBegin(provider, clientID string) (plugins.AuthBeginResult, error) {
	svc, err := a.authService()
	if err != nil {
		return plugins.AuthBeginResult{}, err
	}
	return svc.Begin(a.appContext(), provider, clientID)
}

// AuthPoll performs one redeem attempt.
func (a *App) AuthPoll(provider string) (plugins.AuthPollResult, error) {
	svc, err := a.authService()
	if err != nil {
		return plugins.AuthPollResult{}, err
	}
	return svc.Poll(a.appContext(), provider)
}

// AuthRotate exchanges the current Bearer token for a new one.
func (a *App) AuthRotate(provider string) error {
	svc, err := a.authService()
	if err != nil {
		return err
	}
	return svc.Rotate(a.appContext(), provider)
}

// AuthRevoke revokes the device token and clears local credentials.
func (a *App) AuthRevoke(provider string) error {
	svc, err := a.authService()
	if err != nil {
		return err
	}
	return svc.Revoke(a.appContext(), provider)
}

// AuthStatus is the authoritative login state.
func (a *App) AuthStatus(provider string) (plugins.AuthStatusResult, error) {
	svc, err := a.authService()
	if err != nil {
		return plugins.AuthStatusResult{}, err
	}
	return svc.Status(provider)
}

// AuthMe refreshes the signed-in user profile.
func (a *App) AuthMe(provider string) (plugins.AuthUser, error) {
	svc, err := a.authService()
	if err != nil {
		return plugins.AuthUser{}, err
	}
	return svc.Me(a.appContext(), provider)
}

// AuthModels refreshes the authorized model catalog.
func (a *App) AuthModels(provider string) ([]string, error) {
	svc, err := a.authService()
	if err != nil {
		return nil, err
	}
	return svc.Models(a.appContext(), provider)
}
