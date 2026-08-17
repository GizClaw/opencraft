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
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/sandbox"

	"github.com/GizClaw/opencraft/internal/runtime"
	"github.com/GizClaw/opencraft/internal/tools/permissions"
)

func TestNormaliseCommandUnwrapsShell(t *testing.T) {
	got := NormaliseCommand(sandbox.ExecRequest{
		Command: "/bin/sh",
		Args:    []string{"-c", "git status"},
	})
	if got != "git status" {
		t.Errorf("normalised = %q", got)
	}
}

func TestNewMergesStaticAndFileRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.yaml")
	if err := os.WriteFile(path, []byte(
		"version: v1\nallow:\n  - \"cat *\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := New([]string{"git status"}, path)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Allowlist().Matches(sandbox.ExecRequest{
		Command: "/bin/sh", Args: []string{"-c", "git status"},
	}) {
		t.Error("static rule should match")
	}
	if !m.Allowlist().Matches(sandbox.ExecRequest{
		Command: "cat", Args: []string{"main.go"},
	}) {
		t.Error("file rule should match")
	}
	if m.Allowlist().Matches(sandbox.ExecRequest{
		Command: "rm", Args: []string{"-rf", "/"},
	}) {
		t.Error("unlisted command should not match")
	}
}

func TestAlwaysAllowPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.yaml")
	m, err := New(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AlwaysAllow("go run *"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(data), "go run *") {
		t.Errorf("approvals file = %q", data)
	}
	// Reloading must pick the persisted rule up.
	m2, err := New(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if !m2.Allowlist().Matches(sandbox.ExecRequest{
		Command: "go", Args: []string{"run", "main.go"},
	}) {
		t.Error("persisted rule not loaded")
	}
}

func TestExecPolicyResourceDecodesSettings(t *testing.T) {
	approvals := filepath.Join(t.TempDir(), "approvals.yaml")
	if err := os.WriteFile(approvals, []byte(
		"version: v1\nallow:\n  - \"cat *\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]any{
		"allowed_commands": []string{"git status"},
		"approvals_path":   approvals,
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := (execPolicyResource{}).New(
		context.Background(), resource.Input{Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	mgr, ok := value.(*Manager)
	if !ok {
		t.Fatalf("resource value = %T, want *Manager", value)
	}
	if !mgr.Allowlist().Matches(sandbox.ExecRequest{
		Command: "/bin/sh", Args: []string{"-c", "git status"},
	}) {
		t.Error("static rule should match")
	}
	if !mgr.Allowlist().Matches(sandbox.ExecRequest{
		Command: "cat", Args: []string{"main.go"},
	}) {
		t.Error("approvals file rule should match")
	}
}

func TestExecPolicyResourceEmptyPathInMemory(t *testing.T) {
	value, err := (execPolicyResource{}).New(
		context.Background(), resource.Input{})
	if err != nil {
		t.Fatal(err)
	}
	mgr, ok := value.(*Manager)
	if !ok {
		t.Fatalf("resource value = %T, want *Manager", value)
	}
	if err := mgr.AlwaysAllow("go run *"); err != nil {
		t.Fatal(err)
	}
	if !mgr.Allowlist().Matches(sandbox.ExecRequest{
		Command: "go", Args: []string{"run", "main.go"},
	}) {
		t.Error("in-memory rule should match")
	}
}

func TestApproveDeniesWithoutChoice(t *testing.T) {
	dir := t.TempDir()
	m, err := New(nil, filepath.Join(dir, "approvals.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	host := agent.HostFuncs{AskUserFn: func(
		context.Context, agent.UserPrompt,
	) (agent.UserReply, error) {
		return agent.UserReply{}, nil // empty reply == deny
	}}
	ctx := agent.ContextWithHost(context.Background(), host)
	decision, err := m.Approve(ctx, sandbox.ApprovalRequest{
		Exec: sandbox.ExecRequest{Command: "rm", Args: []string{"-rf", "/"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision != sandbox.Deny {
		t.Errorf("decision = %v, want Deny", decision)
	}
}

func TestApproveAlwaysAddsAndAllows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.yaml")
	m, err := New(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	host := agent.HostFuncs{AskUserFn: func(
		_ context.Context, prompt agent.UserPrompt,
	) (agent.UserReply, error) {
		if prompt.Metadata[runtime.MetaAllowOther] != "false" {
			t.Errorf("approval prompt must disable other: %+v", prompt.Metadata)
		}
		return agent.UserReply{
			Parts: []message.Part{
				message.TextPart{Text: "总是允许"},
			},
			Metadata: map[string]string{
				runtime.MetaChoice: "always",
			},
		}, nil
	}}
	ctx := agent.ContextWithHost(context.Background(), host)
	decision, err := m.Approve(ctx, sandbox.ApprovalRequest{
		Exec: sandbox.ExecRequest{
			Command: "/bin/sh", Args: []string{"-c", "go run main.go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision != sandbox.Allow {
		t.Errorf("decision = %v, want Allow", decision)
	}
	if !m.Allowlist().Matches(sandbox.ExecRequest{
		Command: "go", Args: []string{"run", "main.go"},
	}) {
		t.Error("approved command should be in allowlist")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// grantHost approves every select prompt.
type grantHost struct {
	agent.NoopHost
}

func (grantHost) AskUser(
	context.Context,
	agent.UserPrompt,
) (agent.UserReply, error) {
	return agent.UserReply{Metadata: map[string]string{
		runtime.MetaStatus: string(runtime.ReplyOK),
		runtime.MetaChoice: "grant",
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
		runtime.MetaStatus: string(runtime.ReplyOK),
		runtime.MetaChoice: "deny",
	}}, nil
}

func TestRequestPermissionsGrantsAndPersists(t *testing.T) {
	approvals := filepath.Join(t.TempDir(), "approvals.yaml")
	mgr, err := New(nil, approvals)
	if err != nil {
		t.Fatal(err)
	}
	ctx := agent.ContextWithHost(context.Background(), grantHost{})

	tool := permissions.New(mgr)
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
	ctx := agent.ContextWithHost(context.Background(), denyHost{})

	tool := permissions.New(mgr)
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
	tool := permissions.New(nil)
	_, err := tool.Execute(ctx, `{"permissions":["git push"]}`)
	if err == nil {
		t.Fatal("expected error when runtime has no exec policy")
	}
	if !errdefs.IsNotAvailable(err) {
		t.Fatalf("error = %v, want NotAvailable", err)
	}
}
