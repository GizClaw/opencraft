package webfetch

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestDomainGateAllowDeny(t *testing.T) {
	orig := lookupHost
	lookupHost = func(_ context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	}
	t.Cleanup(func() { lookupHost = orig })

	gate := DomainGate(sandbox.WebFetchPolicy{
		AllowHosts: []string{"example.com", "*.openai.com"},
		DenyHosts:  []string{"bad.example.com"},
	})
	ctx := context.Background()

	for _, tc := range []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"docs.openai.com", true},
		{"bad.example.com", false},
		{"github.com", false},
	} {
		err := gate(ctx, tc.host)
		if (err == nil) != tc.want {
			t.Errorf("gate(%q) = %v, want allowed=%v", tc.host, err, tc.want)
		}
	}
}

func TestDomainGateSSRFGuard(t *testing.T) {
	gate := DomainGate(sandbox.WebFetchPolicy{})
	ctx := context.Background()
	for _, host := range []string{
		"127.0.0.1",
		"10.0.0.5",
		"169.254.169.254",
		"localhost",
	} {
		if err := gate(ctx, host); err == nil {
			t.Errorf("gate(%q) must be blocked by the SSRF guard", host)
		}
	}
	open := DomainGate(sandbox.WebFetchPolicy{AllowPrivate: true})
	if err := open(ctx, "127.0.0.1"); err != nil {
		t.Fatalf("allow_private must permit loopback, got %v", err)
	}
}

func TestYOLOBypassGate(t *testing.T) {
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	inner := DomainGate(sandbox.WebFetchPolicy{}) // SSRF guard on
	gate := YOLOBypassGate(store, inner)

	// No RunInfo: the gate applies.
	if err := gate(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("without RunInfo the gate must apply")
	}

	// Workspace mode: the gate applies.
	ctx := agent.WithRunInfo(context.Background(),
		agent.RunInfo{Identity: agent.Identity{ConversationID: "s-1"}})
	if err := gate(ctx, "127.0.0.1"); err == nil {
		t.Fatal("workspace mode must keep the gate")
	}

	// YOLO mode: the gate is skipped entirely (even deny lists).
	if err := store.SetMode(context.Background(), "s-1", ocsessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}
	if err := gate(ctx, "127.0.0.1"); err != nil {
		t.Fatalf("YOLO mode must bypass the gate, got %v", err)
	}
}
