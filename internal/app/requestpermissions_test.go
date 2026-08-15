package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/interact"
	"github.com/GizClaw/opencraft/internal/tools/requestpermissions"
)

// grantHost approves every select prompt.
type grantHost struct {
	agent.NoopHost
}

func (grantHost) AskUser(
	context.Context,
	agent.UserPrompt,
) (agent.UserReply, error) {
	return agent.UserReply{Metadata: map[string]string{
		interact.MetaStatus: string(interact.ReplyOK),
		interact.MetaChoice: "grant",
	}}, nil
}

// denyHost rejects every select prompt.
type denyHost struct {
	agent.NoopHost
}

func (denyHost) AskUser(
	context.Context,
	agent.UserPrompt,
) (agent.UserReply, error) {
	return agent.UserReply{Metadata: map[string]string{
		interact.MetaStatus: string(interact.ReplyOK),
		interact.MetaChoice: "deny",
	}}, nil
}

func TestRequestPermissionsGrantsAndPersists(t *testing.T) {
	approvals := filepath.Join(t.TempDir(), "approvals.yaml")
	mgr, err := New(nil, approvals)
	if err != nil {
		t.Fatal(err)
	}
	ctx := agent.ContextWithHost(context.Background(), WithExecPolicy(grantHost{}, mgr))

	tool := requestpermissions.New()
	out, err := tool.Execute(ctx,
		`{"permissions":["npm install", "  git   push  "], "reason":"deploy"}`)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Granted     bool     `json:"granted"`
		Permissions []string `json:"permissions"`
		Cancelled   bool     `json:"cancelled"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Granted || res.Cancelled {
		t.Fatalf("result = %+v, want granted", res)
	}
	if len(res.Permissions) != 2 ||
		res.Permissions[0] != "npm install" ||
		res.Permissions[1] != "git push" {
		t.Fatalf("permissions = %v, want normalized rules", res.Permissions)
	}

	rules := mgr.Allowlist().Rules()
	if len(rules) != 2 || rules[0] != "npm install" || rules[1] != "git push" {
		t.Fatalf("allowlist = %v, want granted rules", rules)
	}
	data, err := os.ReadFile(approvals)
	if err != nil {
		t.Fatalf("approvals file: %v", err)
	}
	for _, want := range []string{"npm install", "git push"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("approvals file %q missing %q", data, want)
		}
	}
}

func TestRequestPermissionsDenied(t *testing.T) {
	approvals := filepath.Join(t.TempDir(), "approvals.yaml")
	mgr, err := New(nil, approvals)
	if err != nil {
		t.Fatal(err)
	}
	ctx := agent.ContextWithHost(context.Background(), WithExecPolicy(denyHost{}, mgr))

	tool := requestpermissions.New()
	out, err := tool.Execute(ctx, `{"permissions":["git push"]}`)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Granted     bool     `json:"granted"`
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Granted || len(res.Permissions) != 0 {
		t.Fatalf("result = %+v, want denied", res)
	}
	if len(mgr.Allowlist().Rules()) != 0 {
		t.Fatalf("allowlist = %v, want unchanged", mgr.Allowlist().Rules())
	}
}

func TestRequestPermissionsRequiresExecPolicy(t *testing.T) {
	ctx := agent.ContextWithHost(context.Background(), agent.NoopHost{})
	tool := requestpermissions.New()
	_, err := tool.Execute(ctx, `{"permissions":["git push"]}`)
	if err == nil {
		t.Fatal("expected error when host has no exec policy")
	}
	if !errdefs.IsNotAvailable(err) {
		t.Fatalf("error = %v, want NotAvailable", err)
	}
}
