package requestpermissions

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"

	"github.com/GizClaw/opencraft/internal/runtime"
)

type fakePolicy struct {
	granted []string
}

func (p *fakePolicy) AlwaysAllow(rule string) error {
	p.granted = append(p.granted, rule)
	return nil
}

func (p *fakePolicy) Rules() []string { return nil }

func newCtx(choice string) context.Context {
	return agent.ContextWithHost(context.Background(), agent.HostFuncs{
		Inner: agent.NoopHost{},
		AskUserFn: func(
			_ context.Context,
			_ agent.UserPrompt,
		) (agent.UserReply, error) {
			return agent.UserReply{Metadata: map[string]string{
				runtime.MetaChoice: choice,
			}}, nil
		},
	})
}

func TestRequestPermissionsGrant(t *testing.T) {
	policy := &fakePolicy{}
	out, err := New(policy).Execute(newCtx("grant"),
		`{"permissions":["npm install","git push"],"reason":"deploy"}`)
	if err != nil {
		t.Fatalf("request_permissions: %v", err)
	}
	for _, want := range []string{
		`"granted":true`, `"scope":"session"`,
		`"npm install"`, `"git push"`,
	} {
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
	out, err := New(policy).Execute(newCtx("deny"),
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
	ctx := newCtx("grant")
	if _, err := New(&fakePolicy{}).Execute(ctx, `{"permissions":[]}`); err == nil {
		t.Error("empty permissions should error")
	}
	if _, err := New(&fakePolicy{}).Execute(ctx, `{"permissions":["  "]}`); err == nil {
		t.Error("blank permissions should error")
	}
	// No policy on the tool -> NotAvailable.
	if _, err := New(nil).Execute(ctx, `{"permissions":["ls *"]}`); err == nil {
		t.Error("missing policy should error")
	}
}
