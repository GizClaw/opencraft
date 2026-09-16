package execpolicy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"sigs.k8s.io/yaml"

	ocsandbox "github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/foundation/interact"
)

// escalationHost answers the escalation prompt with choice and records
// the prompt it saw.
func escalationHost(
	t *testing.T, choice string, seen *agent.UserPrompt,
) context.Context {
	t.Helper()
	host := agent.HostFuncs{AskUserFn: func(
		_ context.Context, prompt agent.UserPrompt,
	) (agent.UserReply, error) {
		if seen != nil {
			*seen = prompt
		}
		return agent.UserReply{
			Parts: []message.Part{message.TextPart{Text: choice}},
			Metadata: map[string]string{
				interact.MetaChoice: choice,
			},
		}, nil
	}}
	return agent.ContextWithHost(context.Background(), host)
}

func TestEscalateOnceAllowsWithoutPersisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.yaml")
	m, err := New(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	var prompt agent.UserPrompt
	ctx := escalationHost(t, "escalate_once", &prompt)
	decision, err := m.Escalate(ctx, ocsandbox.EscalationRequest{
		Command: "pip install requests",
		Rule:    "pip install requests",
		Reason:  "the sandbox refused the command (operation not permitted)",
		Detail:  "[Errno 1] Operation not permitted: '/Users/x/.local/lib'",
	})
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if !decision.Allow || decision.Remember {
		t.Fatalf("decision = %+v, want one-shot allow", decision)
	}
	if rules := m.EscalatedRules(); len(rules) != 0 {
		t.Fatalf("one-shot approval persisted rules: %v", rules)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("one-shot approval wrote the approvals file: %v", err)
	}

	// Prompt shape: the user must see the command and get all three
	// answers, with no free-form option.
	if prompt.Source != "opencraft.sandbox.escalation" {
		t.Errorf("prompt source = %q", prompt.Source)
	}
	if got := prompt.Metadata[interact.MetaKind]; got != string(interact.KindSelect) {
		t.Errorf("prompt kind = %q", got)
	}
	if got := prompt.Metadata[interact.MetaAllowOther]; got != "false" {
		t.Errorf("allow_other = %q, want false", got)
	}
	options := prompt.Metadata[interact.MetaOptions]
	for _, want := range []string{"escalate_once", "escalate_always", "deny"} {
		if !strings.Contains(options, want) {
			t.Errorf("prompt options %s missing %q", options, want)
		}
	}
	text, err := message.NormalizePart(prompt.Parts[0])
	if err != nil {
		t.Fatal(err)
	}
	body := text.(message.TextPart).Text
	if !strings.Contains(body, "pip install requests") ||
		!strings.Contains(body, "Operation not permitted") {
		t.Errorf("prompt body = %q", body)
	}
}

func TestEscalateAlwaysPersistsRule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.yaml")
	if err := os.WriteFile(path, []byte(
		"version: v1\nallow:\n  - \"git status\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := New(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := escalationHost(t, "escalate_always", nil)
	decision, err := m.Escalate(ctx, ocsandbox.EscalationRequest{
		Command: "pip install requests",
		Rule:    "pip install requests",
		Reason:  "the sandbox refused the command (operation not permitted)",
	})
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if !decision.Allow || !decision.Remember {
		t.Fatalf("decision = %+v, want remembered allow", decision)
	}

	rules := m.EscalatedRules()
	if len(rules) != 1 || rules[0] != "pip install requests" {
		t.Fatalf("escalated rules = %v", rules)
	}
	if !m.EscalatedAllowed(sandbox.ExecRequest{
		Command: "/bin/sh", Args: []string{"-c", "pip install requests"},
	}) {
		t.Fatal("persisted rule must match the shell-wrapped spawn")
	}
	if m.EscalatedAllowed(sandbox.ExecRequest{
		Command: "pip", Args: []string{"install", "flask"},
	}) {
		t.Fatal("rule must not match a different package")
	}

	// The allow file is untouched: escalations live in their own file,
	// so a downgraded build (which rewrites approvals.yaml from its own
	// narrower struct) cannot drop a remembered rule.
	approvalsData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(approvalsData) != "version: v1\nallow:\n  - \"git status\"\n" {
		t.Fatalf("approvals.yaml was rewritten: %q", approvalsData)
	}
	escalationsPath := filepath.Join(dir, escalationsFileName)
	data, err := os.ReadFile(escalationsPath)
	if err != nil {
		t.Fatalf("escalations file: %v", err)
	}
	var file escalationsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Rules) != 1 || file.Rules[0] != "pip install requests" {
		t.Fatalf("escalate rules = %v", file.Rules)
	}

	// The rule survives a reload from disk.
	reloaded, err := New(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.EscalatedRules(); len(got) != 1 ||
		got[0] != "pip install requests" {
		t.Fatalf("reloaded rules = %v", got)
	}
	if !reloaded.Allowlist().Matches(sandbox.ExecRequest{
		Command: "git", Args: []string{"status"},
	}) {
		t.Fatal("allow rules must survive an escalation write")
	}
}

func TestEscalateWritesAuditTrail(t *testing.T) {
	dir := t.TempDir()
	auditDir := filepath.Join(dir, "audit")
	m, err := NewWithAudit(nil, filepath.Join(dir, "approvals.yaml"), auditDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Escalate(
		escalationHost(t, "escalate_once", nil),
		ocsandbox.EscalationRequest{
			Command: "pip install requests",
			Rule:    "pip install requests",
			Reason:  "the sandbox refused the command (operation not permitted)",
			Detail:  "[Errno 1] Operation not permitted: '/Users/x/.local'",
		}); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if _, err := m.Escalate(
		escalationHost(t, "deny", nil),
		ocsandbox.EscalationRequest{
			Command: "rm -rf /tmp/x",
			Rule:    "rm -rf /tmp/x",
		}); err != nil {
		t.Fatalf("Escalate(deny): %v", err)
	}

	data, err := os.ReadFile(filepath.Join(auditDir, escalationAuditFile))
	if err != nil {
		t.Fatalf("audit file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("audit lines = %d, want 2 (%s)", len(lines), data)
	}
	var once escalationAuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &once); err != nil {
		t.Fatal(err)
	}
	if once.Decision != escalationDecisionOnce ||
		once.Command != "pip install requests" ||
		once.Rule != "pip install requests" ||
		!strings.Contains(once.Detail, "Operation not permitted") ||
		once.Time == "" {
		t.Fatalf("first record = %+v", once)
	}
	var denied escalationAuditEntry
	if err := json.Unmarshal([]byte(lines[1]), &denied); err != nil {
		t.Fatal(err)
	}
	if denied.Decision != escalationDecisionDeny {
		t.Fatalf("second record = %+v", denied)
	}
}

// TestEscalateAuditIsBestEffort pins that an unusable audit path cannot
// fail the escalation itself: the decision is what the user asked for,
// the trail is a record of it.
func TestEscalateAuditIsBestEffort(t *testing.T) {
	dir := t.TempDir()
	// A file where the audit directory should be makes MkdirAll fail.
	blocked := filepath.Join(dir, "audit")
	if err := os.WriteFile(blocked, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewWithAudit(nil, filepath.Join(dir, "approvals.yaml"), blocked)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := m.Escalate(
		escalationHost(t, "escalate_once", nil),
		ocsandbox.EscalationRequest{Command: "pip install x", Rule: "pip install x"})
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if !decision.Allow {
		t.Fatal("audit failure must not change the decision")
	}
}

func TestEscalationsSurviveApprovalsRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.yaml")
	m, err := New(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AlwaysEscalate("pip install"); err != nil {
		t.Fatal(err)
	}
	// The allowlist rewrite path (an "always allow" answer) must not
	// touch the escalation file.
	if err := m.AlwaysAllow("git status"); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("git status"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, escalationsFileName))
	if err != nil {
		t.Fatal(err)
	}
	var file escalationsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Rules) != 1 || file.Rules[0] != "pip install" {
		t.Fatalf("escalation rules = %v", file.Rules)
	}
}

func TestEscalateDeniedKeepsConfinedFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.yaml")
	m, err := New(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := m.Escalate(
		escalationHost(t, "deny", nil),
		ocsandbox.EscalationRequest{
			Command: "pip install requests",
			Rule:    "pip install requests",
		})
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if decision.Allow {
		t.Fatal("deny must not allow the retry")
	}
	if len(m.EscalatedRules()) != 0 {
		t.Fatal("deny must not persist a rule")
	}
}

func TestEscalateWithoutHostFailsClosed(t *testing.T) {
	m, err := New(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Escalate(
		context.Background(),
		ocsandbox.EscalationRequest{Command: "pip install requests"},
	); err == nil {
		t.Fatal("escalation without a host must fail closed")
	}
	if len(m.EscalatedRules()) != 0 {
		t.Fatal("failed escalation must not persist a rule")
	}
}

func TestRemoveEscalatedKeepsAllowRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.yaml")
	m, err := New(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AlwaysAllow("git status"); err != nil {
		t.Fatal(err)
	}
	if err := m.AlwaysEscalate("pip install requests"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveEscalated("pip install requests"); err != nil {
		t.Fatalf("RemoveEscalated: %v", err)
	}
	if len(m.EscalatedRules()) != 0 {
		t.Fatalf("rules = %v, want none", m.EscalatedRules())
	}
	data, err := os.ReadFile(filepath.Join(dir, escalationsFileName))
	if err != nil {
		t.Fatal(err)
	}
	var file escalationsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Rules) != 0 {
		t.Fatalf("escalate rules = %v, want none", file.Rules)
	}
	// The allowlist is the other file and keeps its rule.
	if len(m.Allowlist().Rules()) != 1 ||
		m.Allowlist().Rules()[0] != "git status" {
		t.Fatalf("allow rules = %v", m.Allowlist().Rules())
	}
	// Removing again is a no-op.
	if err := m.RemoveEscalated("pip install requests"); err != nil {
		t.Fatalf("second RemoveEscalated: %v", err)
	}
}
