package sandbox

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
	corenet "github.com/GizClaw/flowcraft/core/utils/net"
)

func TestNetPolicyFactoryDefaults(t *testing.T) {
	value, err := (NetPolicyFactory{}).New(context.Background(), resource.Input{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	pol, ok := value.(Policy)
	if !ok {
		t.Fatalf("factory returned %T, want Policy", value)
	}
	if pol.Exec.Mode != corenet.NetDefault {
		t.Fatalf("exec mode = %v, want NetDefault", pol.Exec.Mode)
	}
	if pol.WebFetch.AllowPrivate {
		t.Fatal("SSRF guard must default to enabled")
	}
}

func TestNetPolicyFactoryAllowList(t *testing.T) {
	value, err := (NetPolicyFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{
			"exec": {
				"mode": "allow-list",
				"allow_hosts": ["github.com", "api.openai.com"],
				"rules": [{"action": "deny", "host": "*.example.com"}]
			},
			"web_fetch": {"allow_private": true}
		}`),
	})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	pol := value.(Policy)
	if pol.Exec.Mode != corenet.NetAllowList {
		t.Fatalf("exec mode = %v, want NetAllowList", pol.Exec.Mode)
	}
	if len(pol.Exec.AllowHosts) != 2 || pol.Exec.AllowHosts[0] != "github.com" {
		t.Fatalf("allow hosts = %v", pol.Exec.AllowHosts)
	}
	if len(pol.Exec.Rules) != 1 || pol.Exec.Rules[0].Action != corenet.NetDeny {
		t.Fatalf("rules = %+v", pol.Exec.Rules)
	}
	if !pol.WebFetch.AllowPrivate {
		t.Fatal("allow_private must be honored")
	}
}

func TestNetPolicyFactoryRejectsBadMode(t *testing.T) {
	_, err := (NetPolicyFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{"exec": {"mode": "banana"}}`),
	})
	if err == nil {
		t.Fatal("unknown mode must be rejected")
	}
}
