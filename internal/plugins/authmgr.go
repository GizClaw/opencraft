package plugins

// AuthService is the device-authorization capability backend. It owns
// the in-memory sessions (device codes never leave Go), the device
// fingerprint, and the token/meta writes into the SecretStore. The
// desktop shell wires the provider lookup and the URL opener; the
// service itself has no wails dependency.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// SecretStore is the minimal credential surface the auth service needs.
// Values are only read/written Go-side.
type SecretStore interface {
	Get(ctx context.Context, name string) (value string, found bool, err error)
	Set(ctx context.Context, name, value string) error
	Delete(ctx context.Context, name string) error
}

// Session is one in-flight device authorization.
type Session struct {
	Provider   string
	DeviceCode string
	Interval   time.Duration
	ExpiresAt  time.Time
}

// SessionManager keeps device codes in process memory, keyed by
// provider.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

// NewSessionManager returns an empty session manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{sessions: map[string]*Session{}}
}

// Begin parks one authorization session.
func (m *SessionManager) Begin(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.Provider] = s
}

// Session returns the active session for provider.
func (m *SessionManager) Session(provider string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[provider]
	return s, ok
}

// Clear removes the session for provider (terminal outcomes only).
func (m *SessionManager) Clear(provider string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, provider)
}

// SessionMeta is the non-secret session metadata kept next to the
// token (user, models, base URL, expiry).
type SessionMeta struct {
	Provider     string    `json:"provider"`
	BaseURL      string    `json:"base_url"`
	DefaultModel string    `json:"default_model"`
	Models       []string  `json:"models"`
	DeviceID     string    `json:"device_id"`
	TokenID      string    `json:"token_id"`
	TokenSuffix  string    `json:"token_suffix"`
	ClientName   string    `json:"client_name"`
	ExpiresAt    time.Time `json:"expires_at"`
	User         AuthUser  `json:"user"`
}

// EncodeMeta renders session metadata for storage.
func EncodeMeta(meta SessionMeta) (string, error) {
	raw, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// DecodeMeta parses stored session metadata.
func DecodeMeta(raw string) (SessionMeta, error) {
	var meta SessionMeta
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return meta, fmt.Errorf("auth: decode meta: %w", err)
	}
	return meta, nil
}

// AuthBeginResult is what the frontend may see after creating an
// authorization: public URLs and timing only.
type AuthBeginResult struct {
	Provider                string    `json:"provider"`
	VerificationURI         string    `json:"verification_uri"`
	VerificationURIComplete string    `json:"verification_uri_complete"`
	UserCode                string    `json:"user_code"`
	IntervalSec             int       `json:"interval_sec"`
	// ExpiresAt is RFC3339 UTC; wails' model generator cannot type
	// time.Time (see desktop/dto.go for the same convention).
	ExpiresAt string `json:"expires_at"`
}

// AuthPollResult is one redeem outcome.
type AuthPollResult struct {
	Status        string    `json:"status"` // ok | pending | slow_down | denied | invalid | expired | rate_limited | error
	Message       string    `json:"message,omitempty"`
	RetryAfterSec int       `json:"retry_after_sec,omitempty"`
	User          *AuthUser `json:"user,omitempty"`
	DefaultModel  string    `json:"default_model,omitempty"`
}

// AuthStatusResult is the authoritative login state.
type AuthStatusResult struct {
	Status       string    `json:"status"` // signed_out | authenticated | expired
	User         *AuthUser `json:"user,omitempty"`
	ExpiresAt    string    `json:"expires_at,omitempty"` // RFC3339 UTC
	DefaultModel string    `json:"default_model,omitempty"`
	ModelCount   int       `json:"model_count,omitempty"`
}

// AuthService implements the device-authorization primitives.
type AuthService struct {
	// Sessions holds in-flight authorizations.
	Sessions *SessionManager
	// Secrets is the credential store (token, fingerprint, meta).
	Secrets SecretStore
	// Provider resolves an adapter by provider name.
	Provider func(name string) (AuthProvider, error)
	// AppVersion identifies the calling client.
	AppVersion string
	// OpenURL opens the verification URL in the system browser (nil-safe).
	OpenURL func(url string)
}

// Begin creates a device authorization, opens the system browser at
// the confirmation URL, and parks the device code in memory.
func (s *AuthService) Begin(ctx context.Context, provider, clientID string) (AuthBeginResult, error) {
	if err := ValidateID(provider); err != nil {
		return AuthBeginResult{}, fmt.Errorf("auth: %w", err)
	}
	if s.Secrets == nil {
		return AuthBeginResult{}, errors.New("opencraft secrets: store is unavailable")
	}
	prov, err := s.Provider(provider)
	if err != nil {
		return AuthBeginResult{}, err
	}
	fp, err := s.deviceFingerprint(ctx, provider)
	if err != nil {
		return AuthBeginResult{}, err
	}
	deviceName := ""
	if hostname, err := hostname(); err == nil {
		deviceName = hostname
	}
	begin, err := prov.Begin(ctx, DeviceBeginRequest{
		ClientID:          clientID,
		DeviceName:        deviceName,
		AppVersion:        s.AppVersion,
		DeviceFingerprint: fp,
		Scope:             DefaultScopes,
	})
	if err != nil {
		return AuthBeginResult{}, err
	}
	s.Sessions.Begin(&Session{
		Provider:   provider,
		DeviceCode: begin.DeviceCode,
		Interval:   begin.Interval,
		ExpiresAt:  begin.ExpiresAt,
	})
	if s.OpenURL != nil && begin.VerificationURIComplete != "" {
		s.OpenURL(begin.VerificationURIComplete)
	}
	return AuthBeginResult{
		Provider:                provider,
		VerificationURI:         begin.VerificationURI,
		VerificationURIComplete: begin.VerificationURIComplete,
		UserCode:                begin.UserCode,
		IntervalSec:             int(begin.Interval.Seconds()),
		ExpiresAt:               formatExpiry(begin.ExpiresAt),
	}, nil
}

// Poll performs one redeem attempt. Protocol outcomes are mapped to
// AuthPollResult.Status; only host/transport failures error.
func (s *AuthService) Poll(ctx context.Context, provider string) (AuthPollResult, error) {
	if err := ValidateID(provider); err != nil {
		return AuthPollResult{}, fmt.Errorf("auth: %w", err)
	}
	sess, ok := s.Sessions.Session(provider)
	if !ok {
		return AuthPollResult{Status: "error", Message: "no active authorization"}, nil
	}
	if time.Now().After(sess.ExpiresAt) {
		s.Sessions.Clear(provider)
		return AuthPollResult{Status: "expired"}, nil
	}
	prov, err := s.Provider(provider)
	if err != nil {
		return AuthPollResult{Status: "error", Message: err.Error()}, nil
	}
	fp, err := s.deviceFingerprint(ctx, provider)
	if err != nil {
		return AuthPollResult{Status: "error", Message: err.Error()}, nil
	}
	res, err := prov.Redeem(ctx, DeviceRedeemRequest{
		DeviceCode:        sess.DeviceCode,
		AppVersion:        s.AppVersion,
		DeviceFingerprint: fp,
	})
	if err != nil {
		return AuthPollResult{Status: "error", Message: err.Error()}, nil
	}
	switch res.Code {
	case CodeOK:
		if res.Token == "" {
			return AuthPollResult{Status: "error", Message: "gateway redeem returned no token"}, nil
		}
		s.Sessions.Clear(provider)
		if err := s.storeSession(ctx, provider, res.Token, res.Meta); err != nil {
			return AuthPollResult{Status: "error", Message: err.Error()}, nil
		}
		return AuthPollResult{
			Status: "ok", User: &res.Meta.User, DefaultModel: res.Meta.DefaultModel,
		}, nil
	case CodePending:
		return AuthPollResult{Status: "pending"}, nil
	case CodeSlowDown:
		wait := sess.Interval
		if res.RetryAfter > wait {
			wait = res.RetryAfter
		}
		return AuthPollResult{Status: "slow_down", RetryAfterSec: int(wait.Seconds())}, nil
	case CodeDenied, CodeInvalid, CodeExpired:
		s.Sessions.Clear(provider)
		var status string
		switch res.Code {
		case CodeInvalid:
			status = "invalid"
		case CodeExpired:
			status = "expired"
		default:
			status = "denied"
		}
		return AuthPollResult{Status: status, Message: res.Message}, nil
	case CodeRateLimited:
		return AuthPollResult{
			Status: "rate_limited", RetryAfterSec: int(res.RetryAfter.Seconds()),
		}, nil
	default:
		return AuthPollResult{Status: "error", Message: res.Code + ": " + res.Message}, nil
	}
}

// Rotate exchanges the current Bearer token for a new one and
// atomically replaces it in the secret store.
func (s *AuthService) Rotate(ctx context.Context, provider string) error {
	if err := ValidateID(provider); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	token, ok, err := s.token(provider)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("auth: not signed in")
	}
	prov, err := s.Provider(provider)
	if err != nil {
		return err
	}
	plain, err := prov.Rotate(ctx, token)
	if err != nil {
		return err
	}
	if err := s.Secrets.Set(ctx, TokenAccount(provider), plain); err != nil {
		return fmt.Errorf("auth: store rotated token: %w", err)
	}
	return nil
}

// Revoke revokes the device token server-side (best-effort) and always
// clears local credentials. The device fingerprint is kept so the next
// login binds the same device.
func (s *AuthService) Revoke(ctx context.Context, provider string) error {
	if err := ValidateID(provider); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if token, ok, err := s.token(provider); err == nil && ok && token != "" {
		if prov, err := s.Provider(provider); err == nil {
			_ = prov.Revoke(ctx, token)
		}
	}
	for _, name := range []string{TokenAccount(provider), MetaAccount(provider)} {
		_ = s.Secrets.Delete(ctx, name)
	}
	s.Sessions.Clear(provider)
	return nil
}

// Status is the authoritative login state for one provider.
func (s *AuthService) Status(provider string) (AuthStatusResult, error) {
	if err := ValidateID(provider); err != nil {
		return AuthStatusResult{}, fmt.Errorf("auth: %w", err)
	}
	meta, ok, err := s.meta(provider)
	if err != nil {
		return AuthStatusResult{}, err
	}
	if !ok {
		if _, tokenOK, err := s.token(provider); err == nil && tokenOK {
			return AuthStatusResult{Status: "authenticated"}, nil
		}
		return AuthStatusResult{Status: "signed_out"}, nil
	}
	status := "authenticated"
	if !meta.ExpiresAt.IsZero() && time.Now().After(meta.ExpiresAt) {
		status = "expired"
	}
	return AuthStatusResult{
		Status:       status,
		User:         &meta.User,
		ExpiresAt:    formatExpiry(meta.ExpiresAt),
		DefaultModel: meta.DefaultModel,
		ModelCount:   len(meta.Models),
	}, nil
}

// formatExpiry renders a time as RFC3339 UTC, or "" for the zero value.
func formatExpiry(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// Me refreshes the signed-in user profile and updates the stored meta.
func (s *AuthService) Me(ctx context.Context, provider string) (AuthUser, error) {
	if err := ValidateID(provider); err != nil {
		return AuthUser{}, fmt.Errorf("auth: %w", err)
	}
	token, ok, err := s.token(provider)
	if err != nil {
		return AuthUser{}, err
	}
	if !ok {
		return AuthUser{}, errors.New("auth: not signed in")
	}
	prov, err := s.Provider(provider)
	if err != nil {
		return AuthUser{}, err
	}
	user, err := prov.Me(ctx, token)
	if err != nil {
		return AuthUser{}, err
	}
	if meta, ok, err := s.meta(provider); err == nil && ok {
		meta.User = user
		_ = s.writeMeta(ctx, provider, meta)
	}
	return user, nil
}

// Models refreshes the authorized model catalog and updates the stored
// meta.
func (s *AuthService) Models(ctx context.Context, provider string) ([]string, error) {
	if err := ValidateID(provider); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	token, ok, err := s.token(provider)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("auth: not signed in")
	}
	prov, err := s.Provider(provider)
	if err != nil {
		return nil, err
	}
	models, err := prov.Models(ctx, token)
	if err != nil {
		return nil, err
	}
	if meta, ok, err := s.meta(provider); err == nil && ok {
		meta.Models = models
		_ = s.writeMeta(ctx, provider, meta)
	}
	return models, nil
}

// ReadMeta returns the stored session metadata for provider.
func (s *AuthService) ReadMeta(provider string) (SessionMeta, bool, error) {
	return s.meta(provider)
}

func (s *AuthService) deviceFingerprint(ctx context.Context, provider string) (string, error) {
	if v, found, err := s.Secrets.Get(ctx, FingerprintAccount(provider)); err == nil && found && len(v) >= 32 {
		return v, nil
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generate device fingerprint: %w", err)
	}
	fp := hex.EncodeToString(raw)
	if err := s.Secrets.Set(ctx, FingerprintAccount(provider), fp); err != nil {
		return "", fmt.Errorf("auth: store device fingerprint: %w", err)
	}
	return fp, nil
}

func (s *AuthService) token(provider string) (string, bool, error) {
	if s.Secrets == nil {
		return "", false, errors.New("opencraft secrets: store is unavailable")
	}
	return s.Secrets.Get(context.Background(), TokenAccount(provider))
}

func (s *AuthService) meta(provider string) (SessionMeta, bool, error) {
	var meta SessionMeta
	if s.Secrets == nil {
		return meta, false, errors.New("opencraft secrets: store is unavailable")
	}
	raw, found, err := s.Secrets.Get(context.Background(), MetaAccount(provider))
	if err != nil || !found {
		return meta, false, err
	}
	decoded, err := DecodeMeta(raw)
	return decoded, true, err
}

func (s *AuthService) writeMeta(ctx context.Context, provider string, meta SessionMeta) error {
	raw, err := EncodeMeta(meta)
	if err != nil {
		return err
	}
	if err := s.Secrets.Set(ctx, MetaAccount(provider), raw); err != nil {
		return fmt.Errorf("auth: store meta: %w", err)
	}
	return nil
}

func (s *AuthService) storeSession(ctx context.Context, provider, token string, meta DeviceMeta) error {
	if err := s.Secrets.Set(ctx, TokenAccount(provider), token); err != nil {
		return fmt.Errorf("auth: store token: %w", err)
	}
	gm := SessionMeta{
		Provider:     provider,
		BaseURL:      meta.BaseURL,
		DefaultModel: meta.DefaultModel,
		Models:       meta.Models,
		DeviceID:     meta.DeviceID,
		TokenID:      meta.TokenID,
		TokenSuffix:  meta.TokenSuffix,
		ClientName:   meta.ClientName,
		ExpiresAt:    meta.ExpiresAt,
		User:         meta.User,
	}
	return s.writeMeta(ctx, provider, gm)
}

// hostname returns the OS hostname (best-effort, for device_name).
var hostname = func() (string, error) {
	return os.Hostname()
}
