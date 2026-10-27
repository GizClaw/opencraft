package sandbox

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
	corenet "github.com/GizClaw/flowcraft/core/utils/netpolicy"
)

func TestNetPolicyFactoryDefaults(t *testing.T) {
	value, err := (NetPolicyFactory{}).New(context.Background(), resource.NewTestInput(nil))
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	pol := value
	if pol.Exec.Mode != corenet.NetDefault {
		t.Fatalf("exec mode = %v, want NetDefault", pol.Exec.Mode)
	}
	if pol.WebFetch.AllowPrivate {
		t.Fatal("SSRF guard must default to enabled")
	}
}

func TestNetPolicyFactoryAllowList(t *testing.T) {
	value, err := (NetPolicyFactory{}).New(context.Background(), resource.NewInput([]byte(`{
			"exec": {
				"mode": "allow-list",
				"allow_hosts": ["github.com", "api.openai.com"],
				"rules": [{"action": "deny", "host": "*.example.com"}]
			},
			"web_fetch": {"allow_private": true}
		}`), nil, nil, nil))
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	pol := value
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
	_, err := (NetPolicyFactory{}).New(context.Background(), resource.NewInput([]byte(`{"exec": {"mode": "banana"}}`), nil, nil, nil))
	if err == nil {
		t.Fatal("unknown mode must be rejected")
	}
}
