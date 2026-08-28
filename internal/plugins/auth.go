package plugins

// Auth provider adapter layer. The haivivi adapter implements the
// private device-authorization protocol from docs/client-applications.md
// (RFC 8628-like but not standard OAuth). Secrets returned by the
// gateway (device_code, plaintext token) are consumed by the auth
// service and never cross the JS boundary.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AuthProvider is the Go-side capability backend for one gateway.
type AuthProvider interface {
	// Begin creates a device authorization. The returned device code is
	// consumed by the caller (in-memory only).
	Begin(ctx context.Context, req DeviceBeginRequest) (DeviceBeginResult, error)
	// Redeem polls one authorization. Protocol outcomes (pending,
	// slow_down, denied, ...) are mapped onto DeviceRedeemResult; only
	// transport/host failures return an error.
	Redeem(ctx context.Context, req DeviceRedeemRequest) (DeviceRedeemResult, error)
	// Rotate exchanges the current Bearer token for a new one and
	// returns its plaintext.
	Rotate(ctx context.Context, token string) (string, error)
	// Revoke invalidates the device token server-side.
	Revoke(ctx context.Context, token string) error
	// Me refreshes the signed-in user profile.
	Me(ctx context.Context, token string) (AuthUser, error)
	// Models returns the authorized model catalog.
	Models(ctx context.Context, token string) ([]string, error)
}

// DeviceBeginRequest is the create-authorization payload.
type DeviceBeginRequest struct {
	ClientID          string
	DeviceName        string
	AppVersion        string
	DeviceFingerprint string
	Scope             []string
}

// DeviceBeginResult is the create-authorization response.
type DeviceBeginResult struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	Interval                time.Duration
	ExpiresAt               time.Time
}

// DeviceRedeemRequest is the redeem payload.
type DeviceRedeemRequest struct {
	ClientID          string
	DeviceCode        string
	AppVersion        string
	DeviceFingerprint string
}

// DeviceRedeemResult carries one redeem outcome. Code is the gateway's
// protocol code (OK / AUTHORIZATION_PENDING / SLOW_DOWN / ...).
type DeviceRedeemResult struct {
	Code       string
	Message    string
	RequestID  string
	RetryAfter time.Duration
	Token      string // plaintext, only present on OK
	Meta       DeviceMeta
}

// DeviceMeta is the non-secret portion of a successful redemption.
type DeviceMeta struct {
	TokenID      string
	TokenLabel   string
	TokenSuffix  string
	TokenType    string
	ExpiresAt    time.Time
	BaseURL      string
	Provider     string
	DefaultModel string
	Models       []string
	DeviceID     string
	ClientID     string
	ClientName   string
	User         AuthUser
}

// AuthUser is the signed-in user profile (non-secret).
type AuthUser struct {
	Name       string `json:"name"`
	Email      string `json:"email"`
	Department string `json:"department"`
}

// Gateway protocol codes.
const (
	CodeOK                = "OK"
	CodePending           = "AUTHORIZATION_PENDING"
	CodeSlowDown          = "SLOW_DOWN"
	CodeDenied            = "AUTHORIZATION_DENIED"
	CodeInvalid           = "DEVICE_AUTHORIZATION_INVALID"
	CodeExpired           = "DEVICE_AUTHORIZATION_EXPIRED"
	CodeRateLimited       = "DEVICE_AUTHORIZATION_RATE_LIMITED"
	CodeClientUnavailable = "CLIENT_APPLICATION_UNAVAILABLE"
	CodeClientDisabled    = "CLIENT_APPLICATION_DISABLED"
	CodeIdentityDisabled  = "IDENTITY_DISABLED"
	CodeInsufficientScope = "insufficient_scope"
	CodeUpgradeRequired   = "client_upgrade_required"
	CodeInvalidToken      = "invalid_api_key"
)

// Default gateway endpoints/identity for the haivivi provider.
const (
	DefaultGatewayBaseURL = "https://ai.haivivi.cn"
	DefaultClientID       = "haivivi-work-macos"
)

// DefaultScopes is the permission set the client requests on device
// authorization.
var DefaultScopes = []string{
	"profile:read",
	"models:read",
	"responses:create",
	"usage:read",
	"device:self_rotate",
	"device:self_revoke",
}

// haiviviGateway is the AuthProvider implementation for the Haivivi
// enterprise AI gateway.
type haiviviGateway struct {
	baseURL    string
	client     *http.Client
	clientID   string
	platform   string
	osVersion  string
	appVersion string
}

var _ AuthProvider = (*haiviviGateway)(nil)

// Device identity is probed once per process (cheap, and the gateway
// expects the real machine identity rather than the Go GOOS constant).
var (
	deviceInfoOnce  sync.Once
	devicePlatform  string
	deviceOSVersion string
)

// NewHaiviviProvider builds the haivivi gateway adapter.
func NewHaiviviProvider(baseURL, clientID, appVersion string) AuthProvider {
	platform, osVersion := detectDeviceInfo()
	return &haiviviGateway{
		baseURL:    strings.TrimRight(baseURL, "/"),
		client:     &http.Client{Timeout: 20 * time.Second},
		clientID:   clientID,
		platform:   platform,
		osVersion:  osVersion,
		appVersion: appVersion,
	}
}

func detectDeviceInfo() (platform, osVersion string) {
	deviceInfoOnce.Do(func() {
		goos := runtime.GOOS
		devicePlatform = mapPlatform(goos)
		deviceOSVersion = detectOSVersion(goos)
	})
	return devicePlatform, deviceOSVersion
}

// mapPlatform maps runtime.GOOS onto the gateway's platform vocabulary
// ("macos" per docs/client-applications.md, not Go's "darwin").
func mapPlatform(goos string) string {
	switch goos {
	case "darwin":
		return "macos"
	case "linux":
		return "linux"
	case "windows":
		return "windows"
	default:
		return goos
	}
}

// detectOSVersion returns the real operating system version, falling
// back to the kernel release and finally the GOOS name. Best-effort:
// a missing value must not block device authorization.
func detectOSVersion(goos string) string {
	switch goos {
	case "darwin":
		if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
			if v := strings.TrimSpace(string(out)); v != "" {
				return v
			}
		}
	case "linux":
		if raw, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
					if s := strings.Trim(strings.TrimSpace(v), `"`); s != "" {
						return s
					}
				}
			}
		}
	case "windows":
		if v := os.Getenv("OS"); v != "" {
			return v
		}
	}
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	return goos
}

func (g *haiviviGateway) Begin(ctx context.Context, req DeviceBeginRequest) (DeviceBeginResult, error) {
	clientID := req.ClientID
	if clientID == "" {
		clientID = g.clientID
	}
	body := map[string]any{
		"client_id":          clientID,
		"platform":           g.platform,
		"device_name":        req.DeviceName,
		"app_version":        req.AppVersion,
		"os_version":         g.osVersion,
		"device_fingerprint": req.DeviceFingerprint,
	}
	if len(req.Scope) > 0 {
		body["scope"] = req.Scope
	}
	var out struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    struct {
			DeviceCode              string `json:"device_code"`
			UserCode                string `json:"user_code"`
			VerificationURI         string `json:"verification_uri"`
			VerificationURIComplete string `json:"verification_uri_complete"`
			ExpiresAt               string `json:"expires_at"`
			Interval                int    `json:"interval"`
		} `json:"data"`
		RequestID string `json:"request_id"`
	}
	if err := g.doJSON(ctx, http.MethodPost, "/api/v1/device-authorizations", "", body, &out); err != nil {
		return DeviceBeginResult{}, err
	}
	if out.Data.DeviceCode == "" {
		return DeviceBeginResult{}, fmt.Errorf(
			"auth: gateway begin response missing device_code (code=%s)", out.Code)
	}
	expires, _ := time.Parse(time.RFC3339, out.Data.ExpiresAt)
	interval := time.Duration(out.Data.Interval) * time.Second
	if interval <= 0 {
		interval = 3 * time.Second
	}
	return DeviceBeginResult{
		DeviceCode:              out.Data.DeviceCode,
		UserCode:                out.Data.UserCode,
		VerificationURI:         out.Data.VerificationURI,
		VerificationURIComplete: out.Data.VerificationURIComplete,
		Interval:                interval,
		ExpiresAt:               expires,
	}, nil
}

func (g *haiviviGateway) Redeem(ctx context.Context, req DeviceRedeemRequest) (DeviceRedeemResult, error) {
	clientID := req.ClientID
	if clientID == "" {
		clientID = g.clientID
	}
	body := map[string]any{
		"device_code":        req.DeviceCode,
		"client_id":          clientID,
		"platform":           g.platform,
		"installer_version":  req.AppVersion,
		"device_fingerprint": req.DeviceFingerprint,
	}
	resp, err := g.request(ctx, http.MethodPost, "/api/v1/device-authorizations/redeem", "", body)
	if err != nil {
		return DeviceRedeemResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return DeviceRedeemResult{}, err
	}
	var out struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    struct {
			AccessToken struct {
				ID     string `json:"id"`
				Label  string `json:"label"`
				Suffix string `json:"suffix"`
			} `json:"access_token"`
			Plaintext    string   `json:"plaintext"`
			TokenType    string   `json:"token_type"`
			ExpiresAt    string   `json:"expires_at"`
			BaseURL      string   `json:"base_url"`
			Provider     string   `json:"provider"`
			DefaultModel string   `json:"default_model"`
			Models       []string `json:"models"`
			DeviceID     string   `json:"device_id"`
			Client       struct {
				ClientID    string `json:"client_id"`
				DisplayName string `json:"display_name"`
			} `json:"client"`
			User AuthUser `json:"user"`
		} `json:"data"`
		RequestID string `json:"request_id"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	result := DeviceRedeemResult{
		Code:      out.Code,
		Message:   out.Message,
		RequestID: out.RequestID,
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			result.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	if out.Code == "" {
		switch resp.StatusCode {
		case http.StatusAccepted:
			result.Code = CodePending
		case http.StatusTooManyRequests:
			result.Code = CodeRateLimited
		default:
			result.Code = fmt.Sprintf("http_%d", resp.StatusCode)
		}
	}
	if out.Code == CodeOK {
		expires, _ := time.Parse(time.RFC3339, out.Data.ExpiresAt)
		result.Token = out.Data.Plaintext
		result.Meta = DeviceMeta{
			TokenID:      out.Data.AccessToken.ID,
			TokenLabel:   out.Data.AccessToken.Label,
			TokenSuffix:  out.Data.AccessToken.Suffix,
			TokenType:    out.Data.TokenType,
			ExpiresAt:    expires,
			BaseURL:      out.Data.BaseURL,
			Provider:     out.Data.Provider,
			DefaultModel: out.Data.DefaultModel,
			Models:       out.Data.Models,
			DeviceID:     out.Data.DeviceID,
			ClientID:     out.Data.Client.ClientID,
			ClientName:   out.Data.Client.DisplayName,
			User:         out.Data.User,
		}
	}
	return result, nil
}

func (g *haiviviGateway) Rotate(ctx context.Context, token string) (string, error) {
	var out struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}
	if err := g.doJSON(ctx, http.MethodPost, "/v1/device-token/rotate", token, map[string]any{}, &out); err != nil {
		return "", err
	}
	if out.Code != CodeOK || out.Data.Plaintext == "" {
		return "", fmt.Errorf("auth: gateway rotate failed: %s", out.Code)
	}
	return out.Data.Plaintext, nil
}

func (g *haiviviGateway) Revoke(ctx context.Context, token string) error {
	var out struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	err := g.doJSON(ctx, http.MethodPost, "/api/v1/device-enrollments/revoke-token", token, map[string]any{}, &out)
	if err != nil {
		return err
	}
	return nil
}

func (g *haiviviGateway) Me(ctx context.Context, token string) (AuthUser, error) {
	var out struct {
		Code string `json:"code"`
		Data struct {
			User AuthUser `json:"user"`
		} `json:"data"`
	}
	if err := g.doJSON(ctx, http.MethodGet, "/v1/me", token, nil, &out); err != nil {
		return AuthUser{}, err
	}
	return out.Data.User, nil
}

func (g *haiviviGateway) Models(ctx context.Context, token string) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := g.doJSON(ctx, http.MethodGet, "/v1/models", token, nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// doJSON sends a JSON request (with the Bearer token when non-empty),
// decodes the envelope and surfaces gateway protocol errors.
func (g *haiviviGateway) doJSON(
	ctx context.Context, method, path, token string, body any, out any,
) error {
	resp, err := g.request(ctx, method, path, token, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if out != nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, out)
	}
	if resp.StatusCode >= 400 {
		var env struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(raw, &env)
		return &AuthError{
			Code:       authErrorCode(env.Code, resp.StatusCode),
			HTTPStatus: resp.StatusCode,
			Message:    env.Message,
			RequestID:  env.RequestID,
		}
	}
	return nil
}

// request builds and sends one gateway request, attaching the Bearer
// token when non-empty.
func (g *haiviviGateway) request(
	ctx context.Context, method, path, token string, body any,
) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return g.client.Do(req)
}

// AuthError is a structured gateway protocol error surfaced to the
// frontend as an error message with a stable code.
type AuthError struct {
	Code       string
	HTTPStatus int
	Message    string
	RequestID  string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("auth gateway: %s (http %d): %s", e.Code, e.HTTPStatus, e.Message)
}

func authErrorCode(code string, status int) string {
	if code != "" {
		return code
	}
	if status == http.StatusUnauthorized {
		return CodeInvalidToken
	}
	if status == http.StatusUpgradeRequired {
		return CodeUpgradeRequired
	}
	if status == http.StatusTooManyRequests {
		return CodeRateLimited
	}
	return fmt.Sprintf("http_%d", status)
}
