// Sandbox exec policy: layered static rules, the workspace-owned
// approvals file under ~/.opencraft/workspaces/<wid>/, and an approver
// that asks the user through the core prompt protocol.
package execpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"sigs.k8s.io/yaml"

	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	ocsandbox "github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/permissions"
	"github.com/GizClaw/opencraft/internal/capabilities/worldstate"
	"github.com/GizClaw/opencraft/internal/foundation/interact"
)

// approvalsFile is the on-disk shape of the workspace-owned
// approvals.yaml under ~/.opencraft/workspaces/<wid>/: dynamically
// approved commands, kept out of the project directory.
type approvalsFile struct {
	Version string   `json:"version"`
	Allow   []string `json:"allow,omitempty"`
}

// escalationsFile is the workspace-owned escalations.yaml, the sibling
// of approvals.yaml holding the commands allowed to run *without* the
// sandbox ("always" answers to an escalation prompt).
//
// It is deliberately a separate file: a build that predates the
// escalation feature rewrites approvals.yaml from its own narrower
// struct, so anything stored there would be dropped on a downgrade.
// Old builds never open escalations.yaml, so a remembered rule
// survives a rollback.
type escalationsFile struct {
	Version string   `json:"version"`
	Rules   []string `json:"rules,omitempty"`
}

// approvalsVersion is the current approvals file schema version.
const approvalsVersion = "v1"

// Manager owns the dynamic command allowlist and its workspace-backed
// approvals file. It is safe for concurrent use while Exec calls are
// in flight.
type Manager struct {
	allowlist *sandbox.Allowlist
	escalated *sandbox.Allowlist
	path      string
	// escalationsPath is the sibling file holding escalation rules.
	// Derived from path so the deploy document keeps one approvals
	// setting; empty when the policy is in-memory only.
	escalationsPath string
	// auditPath receives one JSONL record per escalation decision. The
	// file is best-effort: a failed write never blocks the command.
	auditPath string
	mu        sync.Mutex
	hooks     *hooks.Manager
}

// New loads static rules plus the workspace approvals file (when it
// exists) into one allowlist. Invalid rules abort construction.
func New(rules []string, approvalsPath string) (*Manager, error) {
	return NewWithAudit(rules, approvalsPath, "")
}

// NewWithAudit is New plus the audit trail root: escalation decisions
// are appended to <auditDir>/escalations.jsonl when auditDir is set.
func NewWithAudit(
	rules []string, approvalsPath, auditDir string,
) (*Manager, error) {
	a, err := sandbox.NewAllowlist(rules...)
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft execpolicy: static allowlist: %v", err)
	}
	escalated, err := sandbox.NewAllowlist()
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft execpolicy: escalation allowlist: %v", err)
	}
	m := &Manager{
		allowlist:       a,
		escalated:       escalated,
		path:            approvalsPath,
		escalationsPath: escalationsPathFor(approvalsPath),
	}
	if strings.TrimSpace(auditDir) != "" {
		m.auditPath = filepath.Join(auditDir, escalationAuditFile)
	}
	file, err := m.readFile()
	if err != nil {
		return nil, err
	}
	if len(file.Allow) > 0 {
		if err := a.Add(file.Allow...); err != nil {
			return nil, err
		}
	}
	escalations, err := m.readEscalations()
	if err != nil {
		return nil, err
	}
	if len(escalations.Rules) > 0 {
		if err := escalated.Add(escalations.Rules...); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// escalationsFileName and escalationAuditFile are the workspace-owned
// file names for remembered escalation rules and their audit trail.
const (
	escalationsFileName = "escalations.yaml"
	escalationAuditFile = "escalations.jsonl"
)

// escalationsPathFor keeps escalations.yaml next to approvals.yaml in
// the workspace root. An empty approvals path means the policy is
// in-memory only, so nothing is persisted.
func escalationsPathFor(approvalsPath string) string {
	if strings.TrimSpace(approvalsPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(approvalsPath), escalationsFileName)
}

// Allowlist returns the shared allowlist used by sandbox.WithApproval.
func (m *Manager) Allowlist() *sandbox.Allowlist {
	return m.allowlist
}

// Rules returns the current allowlist rules (static allowed_commands
// plus dynamically approved commands). It feeds the worldstate
// permissions section so the model always sees the live allowlist.
func (m *Manager) Rules() []string {
	return m.allowlist.Rules()
}

// SetHooks wires the external lifecycle hooks fired on permission
// requests (non-blocking; nil disables).
func (m *Manager) SetHooks(h *hooks.Manager) {
	m.mu.Lock()
	m.hooks = h
	m.mu.Unlock()
}

// Approve implements sandbox.ApprovalFunc: it asks the user through
// the core prompt protocol and grows the allowlist when the user
// chooses to always allow the command. Ask failures are fail-closed.
func (m *Manager) Approve(
	ctx context.Context,
	req sandbox.ApprovalRequest,
) (sandbox.Decision, error) {
	// Read-only mode: a known safe read-only command runs without
	// prompting. The classifier is a tripwire — the OS backend already
	// denies any write outside the explicit writable paths, so a false
	// positive here only lets a read attempt through. Anything not
	// proven read-only falls through to the human approver below.
	if req.Exec.Opts.Write == sandbox.WriteReadOnly &&
		sandbox.ClassifySafeReadOnly(req.Exec) {
		return sandbox.Allow, nil
	}
	command := NormaliseCommand(req.Exec)
	m.mu.Lock()
	hookMgr := m.hooks
	m.mu.Unlock()
	if hookMgr != nil {
		hookMgr.Fire(ctx, hooks.EventPermissionRequest, map[string]any{
			"event":   hooks.EventPermissionRequest,
			"tool":    "exec_command",
			"command": command,
			"reason":  req.Reason,
		})
	}
	host, ok := agent.HostFromContext(ctx)
	if !ok {
		return sandbox.Deny, errdefs.NotAvailablef(
			"opencraft execpolicy: no host in tool context")
	}
	opts, err := json.Marshal([]interact.Option{
		{Label: "Allow once", Value: "allow_once"},
		{Label: "Deny", Value: "deny"},
		{Label: "Always allow", Value: "always"},
	})
	if err != nil {
		return sandbox.Deny, err
	}
	reply, err := host.AskUser(ctx, agent.UserPrompt{
		Parts: []message.Part{message.TextPart{
			Text: fmt.Sprintf(
				"Command is not in the sandbox allowlist: %s\nReason: %s",
				command, req.Reason),
		}},
		Source: "opencraft.sandbox.approval",
		Metadata: map[string]string{
			interact.MetaKind:       string(interact.KindSelect),
			interact.MetaTitle:      "Allow running " + command + "?",
			interact.MetaOptions:    string(opts),
			interact.MetaAllowOther: "false",
			interact.MetaSeverity:   string(interact.SeverityNotice),
		},
	})
	if err != nil {
		return sandbox.Deny, err
	}
	switch reply.Metadata[interact.MetaChoice] {
	case "allow_once":
		return sandbox.Allow, nil
	case "always":
		if err := m.AlwaysAllow(command); err != nil {
			return sandbox.Deny, err
		}
		return sandbox.Allow, nil
	default:
		// Empty reply or "deny": reject the call.
		return sandbox.Deny, nil
	}
}

// Escalate asks the user whether one command the OS sandbox refused may
// run on the host instead. It is the second question the approval gate
// deliberately does not answer: the gate decides whether a command may
// run at all, never under which confine, so a refusal that is really
// "this program needed to write outside the workspace" needs its own
// answer.
//
// Errors are fail-closed: callers treat a failed escalation as "no
// escalation" and keep the original confined failure.
func (m *Manager) Escalate(
	ctx context.Context,
	req ocsandbox.EscalationRequest,
) (ocsandbox.EscalationDecision, error) {
	m.mu.Lock()
	hookMgr := m.hooks
	m.mu.Unlock()
	if hookMgr != nil {
		hookMgr.Fire(ctx, hooks.EventPermissionRequest, map[string]any{
			"event":      hooks.EventPermissionRequest,
			"tool":       "exec_command",
			"command":    req.Command,
			"reason":     req.Reason,
			"escalation": true,
		})
	}
	host, ok := agent.HostFromContext(ctx)
	if !ok {
		return ocsandbox.EscalationDecision{}, errdefs.NotAvailablef(
			"opencraft execpolicy: no host in tool context")
	}
	opts, err := json.Marshal([]interact.Option{
		{Label: "Run outside the sandbox (once)", Value: "escalate_once"},
		{Label: "Deny", Value: "deny"},
		{
			Label: "Always run this command outside the sandbox",
			Value: "escalate_always",
		},
	})
	if err != nil {
		return ocsandbox.EscalationDecision{}, err
	}
	body := fmt.Sprintf(
		"The sandbox refused this command:\n\n%s\n\n%s\n\n"+
			"Approving re-runs the whole command on the host with "+
			"full access: no filesystem sandbox and the full "+
			"environment.",
		req.Command, escalationBody(req))
	reply, err := host.AskUser(ctx, agent.UserPrompt{
		Parts:  []message.Part{message.TextPart{Text: body}},
		Source: "opencraft.sandbox.escalation",
		Metadata: map[string]string{
			interact.MetaKind:       string(interact.KindSelect),
			interact.MetaTitle:      "Run outside the sandbox?",
			interact.MetaOptions:    string(opts),
			interact.MetaAllowOther: "false",
			// The one prompt that hands a command the whole host.
			interact.MetaSeverity: string(interact.SeverityDanger),
		},
	})
	if err != nil {
		return ocsandbox.EscalationDecision{}, err
	}
	switch reply.Metadata[interact.MetaChoice] {
	case "escalate_once":
		m.recordEscalation(req, escalationDecisionOnce)
		return ocsandbox.EscalationDecision{Allow: true}, nil
	case "escalate_always":
		rule := strings.TrimSpace(req.Rule)
		if rule == "" {
			return ocsandbox.EscalationDecision{}, errdefs.Validationf(
				"opencraft execpolicy: escalation rule is empty")
		}
		if err := m.AlwaysEscalate(rule); err != nil {
			return ocsandbox.EscalationDecision{}, err
		}
		m.recordEscalation(req, escalationDecisionAlways)
		return ocsandbox.EscalationDecision{
			Allow:    true,
			Remember: true,
		}, nil
	default:
		// Empty reply or "deny": keep the confined failure.
		m.recordEscalation(req, escalationDecisionDeny)
		return ocsandbox.EscalationDecision{}, nil
	}
}

// Escalation audit decisions, as written to escalations.jsonl.
const (
	escalationDecisionOnce   = "once"
	escalationDecisionAlways = "always"
	escalationDecisionDeny   = "deny"
)

// escalationAuditEntry is one line of <auditDir>/escalations.jsonl: what
// the sandbox refused and what the user decided. Runs that follow a
// remembered rule leave a grant record here and their actual execution
// in the rollout, which carries the tool result's escalation note.
type escalationAuditEntry struct {
	Time     string `json:"time"`
	Command  string `json:"command"`
	Rule     string `json:"rule,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Decision string `json:"decision"`
}

// recordEscalation appends one decision. It is best-effort by design:
// an audit write failure is logged and never blocks the command the
// user already approved.
func (m *Manager) recordEscalation(
	req ocsandbox.EscalationRequest, decision string,
) {
	path := m.auditPath
	if path == "" {
		return
	}
	data, err := json.Marshal(escalationAuditEntry{
		Time:     time.Now().UTC().Format(time.RFC3339Nano),
		Command:  req.Command,
		Rule:     req.Rule,
		Reason:   req.Reason,
		Detail:   req.Detail,
		Decision: decision,
	})
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: marshal escalation audit record failed", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: create escalation audit dir failed", err)
		return
	}
	file, err := os.OpenFile(
		path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: open escalation audit file failed", err)
		return
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: append escalation audit record failed", err)
	}
	if err := file.Close(); err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: close escalation audit file failed", err)
	}
}

// escalationBody renders why the command looked sandbox-refused,
// including the concrete stderr line when one was captured.
func escalationBody(req ocsandbox.EscalationRequest) string {
	reason := strings.TrimSpace(req.Reason)
	detail := strings.TrimSpace(req.Detail)
	switch {
	case reason == "" && detail == "":
		return "The command failed with a permission error."
	case detail == "":
		return reason + "."
	case reason == "":
		return detail
	default:
		return reason + ":\n\n" + detail
	}
}

// AlwaysAllow adds a rule to the allowlist and persists it to the
// workspace approvals file. The in-memory update and the file
// read-modify-write share one lock so concurrent approvals cannot lose
// each other's persisted rules.
func (m *Manager) AlwaysAllow(rule string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	before := m.allowlist.Rules()
	if err := m.allowlist.Add(rule); err != nil {
		return err
	}
	file, err := m.readFile()
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: rollback allowlist after read failure",
			m.allowlist.Set(before))
		return err
	}
	file.Allow = appendRule(file.Allow, rule)
	if err := m.writeFile(file); err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: rollback allowlist after write failure",
			m.allowlist.Set(before))
		return err
	}
	return nil
}

// AlwaysEscalate adds a rule to the escalation list and persists it.
// A matching command runs on the host without the sandbox: it skips the
// confined attempt and the approval prompt entirely.
func (m *Manager) AlwaysEscalate(rule string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	before := m.escalated.Rules()
	if err := m.escalated.Add(rule); err != nil {
		return err
	}
	file, err := m.readEscalations()
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: rollback escalation rules after read failure",
			m.escalated.Set(before))
		return err
	}
	file.Rules = appendRule(file.Rules, rule)
	if err := m.writeEscalations(file); err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: rollback escalation rules after write failure",
			m.escalated.Set(before))
		return err
	}
	return nil
}

// appendRule appends rule unless it is already present, so repeated
// "always allow" answers do not grow the file.
func appendRule(rules []string, rule string) []string {
	for _, r := range rules {
		if r == rule {
			return rules
		}
	}
	return append(rules, rule)
}

// removeRule returns rules without rule.
func removeRule(rules []string, rule string) []string {
	out := rules[:0:0]
	for _, r := range rules {
		if r != rule {
			out = append(out, r)
		}
	}
	return out
}

// Remove deletes a rule from the allowlist and persists the change.
// It is a no-op when the rule is not present.
func (m *Manager) Remove(rule string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rules := m.allowlist.Rules()
	filtered := removeRule(rules, rule)
	if len(filtered) == len(rules) {
		return nil
	}
	if err := m.allowlist.Set(filtered); err != nil {
		return err
	}
	file, err := m.readFile()
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: rollback allowlist after read failure",
			m.allowlist.Set(rules))
		return err
	}
	file.Allow = removeRule(file.Allow, rule)
	if err := m.writeFile(file); err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: rollback allowlist after write failure",
			m.allowlist.Set(rules))
		return err
	}
	return nil
}

// RemoveEscalated deletes a rule from the escalation list and persists
// the change. It is a no-op when the rule is not present.
func (m *Manager) RemoveEscalated(rule string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rules := m.escalated.Rules()
	filtered := removeRule(rules, rule)
	if len(filtered) == len(rules) {
		return nil
	}
	if err := m.escalated.Set(filtered); err != nil {
		return err
	}
	file, err := m.readEscalations()
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: rollback escalation rules after read failure",
			m.escalated.Set(rules))
		return err
	}
	file.Rules = removeRule(file.Rules, rule)
	if err := m.writeEscalations(file); err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: rollback escalation rules after write failure",
			m.escalated.Set(rules))
		return err
	}
	return nil
}

// EscalatedRules returns the commands allowed to run without the
// sandbox.
func (m *Manager) EscalatedRules() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.escalated.Rules()
}

// EscalatedAllowed reports whether req matches a persisted escalation
// rule. The sandbox runner consults it before the confined attempt.
func (m *Manager) EscalatedAllowed(req sandbox.ExecRequest) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.escalated.Matches(req)
}

// NormaliseCommand renders the normalized token list of an ExecRequest
// as the allowlist rule string ("sh -c" wrappers are unwrapped).
func NormaliseCommand(req sandbox.ExecRequest) string {
	return strings.Join(sandbox.NormaliseExec(req), " ")
}

func (m *Manager) readFile() (approvalsFile, error) {
	if m.path == "" {
		return approvalsFile{Version: approvalsVersion}, nil
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return approvalsFile{Version: approvalsVersion}, nil
		}
		return approvalsFile{}, err
	}
	var f approvalsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return approvalsFile{}, errdefs.Validationf(
			"opencraft execpolicy: parse %s: %v", m.path, err)
	}
	return f, nil
}

func (m *Manager) writeFile(file approvalsFile) error {
	if m.path == "" {
		return nil
	}
	file.Version = approvalsVersion
	return writeYAML(m.path, file)
}

// readEscalations loads escalations.yaml, treating a missing file as an
// empty rule set.
func (m *Manager) readEscalations() (escalationsFile, error) {
	if m.escalationsPath == "" {
		return escalationsFile{Version: approvalsVersion}, nil
	}
	data, err := os.ReadFile(m.escalationsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return escalationsFile{Version: approvalsVersion}, nil
		}
		return escalationsFile{}, err
	}
	var f escalationsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return escalationsFile{}, errdefs.Validationf(
			"opencraft execpolicy: parse %s: %v", m.escalationsPath, err)
	}
	return f, nil
}

func (m *Manager) writeEscalations(file escalationsFile) error {
	if m.escalationsPath == "" {
		return nil
	}
	file.Version = approvalsVersion
	return writeYAML(m.escalationsPath, file)
}

// writeYAML atomically replaces path with the marshalled document: the
// temp file lives in the same directory so the rename stays on one
// filesystem, matching the approvals file's original behaviour.
func writeYAML(path string, doc any) error {
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".opencraft-policy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err := os.Remove(tmpName); err != nil && !os.IsNotExist(err) {
			telemetry.WarnErr(context.Background(),
				"opencraft execpolicy: remove policy temp file failed", err)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		telemetry.WarnErr(context.Background(),
			"opencraft execpolicy: close policy temp after write failure",
			tmp.Close())
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ---------------------------------------------------------------------------
// Deploy resource: the execpolicy resource owns the policy manager.
// ---------------------------------------------------------------------------

// execPolicySettings is the deploy-document shape of the execpolicy
// resource: the static command rules plus the path of the project
// approvals file whose path is injected by the Host.
// An empty approvals_path keeps the policy in-memory only.
type execPolicySettings struct {
	AllowedCommands []string `json:"allowed_commands,omitempty"`
	ApprovalsPath   string   `json:"approvals_path"`
	// AuditDir receives escalations.jsonl (escalation decisions).
	// Empty disables the trail.
	AuditDir string `json:"audit_dir,omitempty"`
}

// execPolicyResource is the opencraft.execpolicy deploy resource. It
// owns the sandbox exec policy: static rules plus the project
// approvals file merge into one allowlist, and every consumer (the
// sandbox runner, the worldstate permissions section, and the
// request_permissions tool) depends on this resource instead of
// building its own manager.
type execPolicyResource struct{}

// Register adds the opencraft.execpolicy deploy resource factory.
func Register(r *resource.Registry) error {
	return r.Register(execPolicyResource{})
}

var _ resource.Factory = execPolicyResource{}

func (execPolicyResource) Spec() resource.Spec {
	return resource.Spec{
		Kind: "opencraft.execpolicy",
		Impl: "manager",
		Deps: []resource.DepSpec{{
			Name: "hooks", Type: hooks.ResourceKind, Required: false,
		}},
	}
}

func (execPolicyResource) New(
	ctx context.Context,
	in resource.Input,
) (any, error) {
	settings, err := resource.DecodeTyped[execPolicySettings](
		ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft execpolicy: decode settings: %v", err)
	}
	mgr, err := NewWithAudit(
		settings.AllowedCommands, settings.ApprovalsPath, settings.AuditDir)
	if err != nil {
		return nil, err
	}
	if dep, ok := in.Dep("hooks"); ok {
		if hookMgr, ok := dep.(*hooks.Manager); ok {
			mgr.SetHooks(hookMgr)
		}
	}
	return mgr, nil
}

var _ permissions.Policy = (*Manager)(nil)
var _ worldstate.PrefixProvider = (*Manager)(nil)
var _ ocsandbox.Escalator = (*Manager)(nil)
var _ ocsandbox.EscalationRules = (*Manager)(nil)
