package requestpermissions

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"

	"github.com/GizClaw/opencraft/internal/interact"
)

type fakePolicy struct {
	granted []string
}

func (p *fakePolicy) AlwaysAllow(rule string) error {
	p.granted = append(p.granted, rule)
	return nil
}

func (p *fakePolicy) Rules() []string { return nil }

type policyHost struct {
	agent.Host
	policy Policy
}

func (h policyHost) ExecPolicy() Policy     { return h.policy }
func (h policyHost) UnwrapHost() agent.Host { return h.Host }

func newCtx(choice string, policy Policy) context.Context {
	var host agent.Host = agent.HostFuncs{
		Inner: agent.NoopHost{},
		AskUserFn: func(
			_ context.Context,
			_ agent.UserPrompt,
		) (agent.UserReply, error) {
			return agent.UserReply{Metadata: map[string]string{
				interact.MetaChoice: choice,
			}}, nil
		},
	}
	if policy != nil {
		host = policyHost{Host: host, policy: policy}
	}
	return agent.ContextWithHost(context.Background(), host)
}

func TestRequestPermissionsGrant(t *testing.T) {
	policy := &fakePolicy{}
	out, err := New().Execute(newCtx("grant", policy),
		`{"permissions":["npm install","git push"],"reason":"deploy"}`)
	if err != nil {
		t.Fatalf("request_permissions: %v", err)
	}
	for _, want := range []string{`"granted":true`, `"scope":"session"`,
		`"npm install"`, `"git push"`} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %s: %s", want, out)
		}
	}
	if len(policy.granted) != 2 {
		t.Errorf("expected 2 granted rules, got %v", policy.granted)
	}
}

func TestRequestPermissionsDeny(t *testing.T) {
	policy := &fakePolicy{}
	out, err := New().Execute(newCtx("deny", policy),
		`{"permissions":["rm -rf /"]}`)
	if err != nil {
		t.Fatalf("request_permissions: %v", err)
	}
	if !strings.Contains(out, `"granted":false`) {
		t.Errorf("deny result: %s", out)
	}
	if len(policy.granted) != 0 {
		t.Errorf("deny should not grant: %v", policy.granted)
	}
}

func TestRequestPermissionsValidation(t *testing.T) {
	ctx := newCtx("grant", &fakePolicy{})
	if _, err := New().Execute(ctx, `{"permissions":[]}`); err == nil {
		t.Error("empty permissions should error")
	}
	if _, err := New().Execute(ctx, `{"permissions":["  "]}`); err == nil {
		t.Error("blank permissions should error")
	}
	// No policy on the host -> NotAvailable.
	plain := agent.ContextWithHost(context.Background(),
		agent.HostFuncs{Inner: agent.NoopHost{}})
	if _, err := New().Execute(plain, `{"permissions":["ls *"]}`); err == nil {
		t.Error("missing policy should error")
	}
}

var _ PolicyProvider = policyHost{}
