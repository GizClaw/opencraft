package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/sandbox"

	"github.com/GizClaw/opencraft/internal/interact"
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
		if prompt.Metadata[interact.MetaAllowOther] != "false" {
			t.Errorf("approval prompt must disable other: %+v", prompt.Metadata)
		}
		return agent.UserReply{
			Parts: []message.Part{
				message.TextPart{Text: "总是允许"},
			},
			Metadata: map[string]string{
				interact.MetaChoice: "always",
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
