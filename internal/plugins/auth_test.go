package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// memorySecrets is an in-memory SecretStore for tests.
type memorySecrets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memorySecrets) Get(_ context.Context, name string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[name]
	return v, ok, nil
}

func (s *memorySecrets) Set(_ context.Context, name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[name] = value
	return nil
}

func (s *memorySecrets) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, name)
	return nil
}

// scriptedGateway implements the haivivi protocol endpoints against a
// fake redeem sequence.
type scriptedGateway struct {
	redeemCodes []string
	redeemIndex int
	token       string
}

func (s *scriptedGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1/device-authorizations":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "OK", "message": "success",
			"data": map[string]any{
				"device_code":               "secret-code",
				"user_code":                 "ABCD-EFGH",
				"verification_uri":          "https://ai.haivivi.cn/device/authorize",
				"verification_uri_complete": "https://ai.haivivi.cn/device/authorize?code=ABCD-EFGH",
				"expires_at":                "2099-01-01T00:00:00Z",
				"interval":                  3,
			},
			"request_id": "req_begin",
		})
	case "/api/v1/device-authorizations/redeem":
		if s.redeemIndex < len(s.redeemCodes) {
			code := s.redeemCodes[s.redeemIndex]
			s.redeemIndex++
			switch code {
			case CodePending:
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": CodePending, "message": "pending", "request_id": "req_p",
				})
			case CodeOK:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": "OK", "message": "success",
					"data": map[string]any{
						"access_token":  map[string]any{"id": "tok-1", "label": "l", "suffix": "Ab12"},
						"plaintext":     s.token,
						"token_type":    "Bearer",
						"expires_at":    "2099-06-01T00:00:00Z",
						"base_url":      "https://ai.haivivi.cn/v1",
						"provider":      "haivivi-ai",
						"default_model": "deepseek-flash",
						"models":        []string{"deepseek-flash", "deepseek-vision"},
						"device_id":     "device-1",
						"client":        map[string]any{"client_id": "haivivi-work-macos", "display_name": "Haivivi Work"},
						"user":          map[string]any{"name": "Richard", "email": "r@example.com", "department": "Eng"},
					},
					"request_id": "req_ok",
				})
			case CodeDenied:
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": CodeDenied, "message": "denied", "request_id": "req_d",
				})
			}
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	case "/v1/device-token/rotate":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "OK", "data": map[string]any{"plaintext": "aig_rotated"},
		})
	case "/api/v1/device-enrollments/revoke-token":
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "OK", "message": "revoked"})
	case "/v1/me":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "OK", "data": map[string]any{
				"user": map[string]any{"name": "Richard", "email": "r@example.com", "department": "Eng"},
			},
		})
	case "/v1/models":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "deepseek-flash"},
				{"id": "deepseek-vision"},
			},
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestHaiviviAdapterProtocol(t *testing.T) {
	srv := httptest.NewServer(&scriptedGateway{
		redeemCodes: []string{CodePending, CodeOK},
		token:       "aig_secret_value",
	})
	defer srv.Close()
	g := NewHaiviviProvider(srv.URL, "haivivi-work-macos", "0.1.0")

	begin, err := g.Begin(context.Background(), DeviceBeginRequest{
		DeviceFingerprint: "fp-32-chars-long-abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if begin.DeviceCode != "secret-code" || begin.UserCode != "ABCD-EFGH" {
		t.Fatalf("begin = %+v", begin)
	}
	if begin.Interval != 3*time.Second {
		t.Fatalf("interval = %v", begin.Interval)
	}

	req := DeviceRedeemRequest{DeviceCode: begin.DeviceCode, DeviceFingerprint: "fp"}
	pending, err := g.Redeem(context.Background(), req)
	if err != nil || pending.Code != CodePending {
		t.Fatalf("Redeem(pending) = (%+v, %v)", pending, err)
	}
	ok, err := g.Redeem(context.Background(), req)
	if err != nil {
		t.Fatalf("Redeem(ok): %v", err)
	}
	if ok.Code != CodeOK || ok.Token != "aig_secret_value" {
		t.Fatalf("redeem ok = %+v", ok)
	}
	if ok.Meta.BaseURL != "https://ai.haivivi.cn/v1" || len(ok.Meta.Models) != 2 ||
		ok.Meta.User.Email != "r@example.com" {
		t.Fatalf("meta = %+v", ok.Meta)
	}
	if plain, err := g.Rotate(context.Background(), "aig_secret_value"); err != nil || plain != "aig_rotated" {
		t.Fatalf("Rotate = (%q, %v)", plain, err)
	}
	if err := g.Revoke(context.Background(), "aig_secret_value"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	user, err := g.Me(context.Background(), "aig_secret_value")
	if err != nil || user.Email != "r@example.com" {
		t.Fatalf("Me = (%+v, %v)", user, err)
	}
	models, err := g.Models(context.Background(), "aig_secret_value")
	if err != nil || len(models) != 2 {
		t.Fatalf("Models = (%v, %v)", models, err)
	}
}

// fakeProvider is a scripted AuthProvider for AuthService tests.
type fakeProvider struct {
	mu          sync.Mutex
	redeemSeq   []DeviceRedeemResult
	redeemIndex int
	beginRes    DeviceBeginResult
	rotatePlain string
	revoked     string
	meUser      AuthUser
	models      []string
}

func (f *fakeProvider) Begin(_ context.Context, _ DeviceBeginRequest) (DeviceBeginResult, error) {
	return f.beginRes, nil
}

func (f *fakeProvider) Redeem(_ context.Context, _ DeviceRedeemRequest) (DeviceRedeemResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.redeemIndex < len(f.redeemSeq) {
		res := f.redeemSeq[f.redeemIndex]
		f.redeemIndex++
		return res, nil
	}
	return DeviceRedeemResult{Code: CodePending}, nil
}

func (f *fakeProvider) Rotate(_ context.Context, _ string) (string, error) {
	return f.rotatePlain, nil
}

func (f *fakeProvider) Revoke(_ context.Context, token string) error {
	f.mu.Lock()
	f.revoked = token
	f.mu.Unlock()
	return nil
}

func (f *fakeProvider) Me(_ context.Context, _ string) (AuthUser, error) {
	return f.meUser, nil
}

func (f *fakeProvider) Models(_ context.Context, _ string) ([]string, error) {
	return f.models, nil
}

func newAuthService(f *fakeProvider, sec SecretStore) *AuthService {
	return &AuthService{
		Sessions:   NewSessionManager(),
		Secrets:    sec,
		AppVersion: "0.1.0",
		Provider: func(name string) (AuthProvider, error) {
			return f, nil
		},
	}
}

func TestAuthServiceBeginKeepsDeviceCodeInMemory(t *testing.T) {
	sec := &memorySecrets{}
	f := &fakeProvider{beginRes: DeviceBeginResult{
		DeviceCode: "device-code", UserCode: "ABCD-EFGH",
		VerificationURIComplete: "https://x/verify?code=ABCD-EFGH",
		Interval:                3 * time.Second,
		ExpiresAt:               time.Now().Add(5 * time.Minute),
	}}
	svc := newAuthService(f, sec)
	var opened string
	svc.OpenURL = func(u string) { opened = u }
	res, err := svc.Begin(context.Background(), "haivivi", "")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if res.UserCode != "ABCD-EFGH" || res.IntervalSec != 3 || opened == "" {
		t.Fatalf("begin = %+v, opened = %q", res, opened)
	}
	if _, ok := svc.Sessions.Session("haivivi"); !ok {
		t.Fatal("session not parked")
	}
	fp, found, err := sec.Get(context.Background(), FingerprintAccount("haivivi"))
	if err != nil || !found || len(fp) < 32 {
		t.Fatalf("fingerprint = (%q, %v, %v)", fp, found, err)
	}
}

func TestAuthServicePollOkStoresTokenAndMeta(t *testing.T) {
	sec := &memorySecrets{}
	f := &fakeProvider{
		beginRes: DeviceBeginResult{
			DeviceCode: "device-code", Interval: 3 * time.Second,
			ExpiresAt: time.Now().Add(5 * time.Minute),
		},
		redeemSeq: []DeviceRedeemResult{{
			Code:  CodeOK,
			Token: "aig_secret",
			Meta: DeviceMeta{
				BaseURL:      "https://ai.haivivi.cn/v1",
				DefaultModel: "deepseek-flash",
				Models:       []string{"deepseek-flash", "deepseek-vision"},
				ExpiresAt:    time.Now().Add(24 * time.Hour),
				User:         AuthUser{Name: "Richard", Email: "r@example.com"},
			},
		}},
	}
	svc := newAuthService(f, sec)
	if _, err := svc.Begin(context.Background(), "haivivi", ""); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Poll(context.Background(), "haivivi")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Status != "ok" || res.User == nil || res.User.Email != "r@example.com" {
		t.Fatalf("poll = %+v", res)
	}
	token, found, _ := sec.Get(context.Background(), TokenAccount("haivivi"))
	if !found || token != "aig_secret" {
		t.Fatalf("token = (%q, %v)", token, found)
	}
	if _, ok := svc.Sessions.Session("haivivi"); ok {
		t.Fatal("session not cleared after success")
	}
	st, err := svc.Status("haivivi")
	if err != nil || st.Status != "authenticated" || st.ModelCount != 2 {
		t.Fatalf("status = %+v, err = %v", st, err)
	}
}

func TestAuthServicePollProtocolOutcomes(t *testing.T) {
	sec := &memorySecrets{}
	f := &fakeProvider{
		beginRes: DeviceBeginResult{
			DeviceCode: "d", Interval: 3 * time.Second,
			ExpiresAt: time.Now().Add(5 * time.Minute),
		},
	}
	svc := newAuthService(f, sec)
	f.redeemSeq = []DeviceRedeemResult{
		{Code: CodePending},
		{Code: CodeSlowDown, RetryAfter: 5 * time.Second},
	}
	if _, err := svc.Begin(context.Background(), "haivivi", ""); err != nil {
		t.Fatal(err)
	}
	p, _ := svc.Poll(context.Background(), "haivivi")
	if p.Status != "pending" {
		t.Fatalf("pending = %q", p.Status)
	}
	sd, _ := svc.Poll(context.Background(), "haivivi")
	if sd.Status != "slow_down" || sd.RetryAfterSec != 5 {
		t.Fatalf("slow_down = %+v", sd)
	}
	f.redeemSeq = []DeviceRedeemResult{{Code: CodeDenied}}
	f.redeemIndex = 0
	if _, err := svc.Begin(context.Background(), "haivivi", ""); err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Poll(context.Background(), "haivivi")
	if d.Status != "denied" {
		t.Fatalf("denied = %q", d.Status)
	}
	if _, ok := svc.Sessions.Session("haivivi"); ok {
		t.Fatal("session not cleared on denied")
	}
}

func TestAuthServiceRotateRevokeMeModels(t *testing.T) {
	sec := &memorySecrets{}
	f := &fakeProvider{
		beginRes: DeviceBeginResult{DeviceCode: "d", ExpiresAt: time.Now().Add(5 * time.Minute)},
		redeemSeq: []DeviceRedeemResult{{
			Code: CodeOK, Token: "aig_old",
			Meta: DeviceMeta{BaseURL: "https://ai.haivivi.cn/v1", Models: []string{"m"}},
		}},
		rotatePlain: "aig_rotated",
		meUser:      AuthUser{Name: "Richard", Email: "r@example.com"},
		models:      []string{"deepseek-flash", "deepseek-vision"},
	}
	svc := newAuthService(f, sec)
	if _, err := svc.Begin(context.Background(), "haivivi", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Poll(context.Background(), "haivivi"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rotate(context.Background(), "haivivi"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	token, found, _ := sec.Get(context.Background(), TokenAccount("haivivi"))
	if !found || token != "aig_rotated" {
		t.Fatalf("rotated = (%q, %v)", token, found)
	}
	user, err := svc.Me(context.Background(), "haivivi")
	if err != nil || user.Email != "r@example.com" {
		t.Fatalf("Me = (%+v, %v)", user, err)
	}
	models, err := svc.Models(context.Background(), "haivivi")
	if err != nil || len(models) != 2 {
		t.Fatalf("Models = (%v, %v)", models, err)
	}
	if err := svc.Revoke(context.Background(), "haivivi"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	f.mu.Lock()
	revoked := f.revoked
	f.mu.Unlock()
	if revoked != "aig_rotated" {
		t.Fatalf("revoked = %q", revoked)
	}
	if _, found, _ := sec.Get(context.Background(), TokenAccount("haivivi")); found {
		t.Fatal("token still present after revoke")
	}
	st, _ := svc.Status("haivivi")
	if st.Status != "signed_out" {
		t.Fatalf("status after revoke = %q", st.Status)
	}
}
