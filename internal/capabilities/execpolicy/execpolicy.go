// Sandbox exec policy: layered static rules, a project-scoped approvals
// file, and an approver that asks the user through the core prompt
// protocol.
package execpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"sigs.k8s.io/yaml"

	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/permissions"
	"github.com/GizClaw/opencraft/internal/capabilities/worldstate"
	"github.com/GizClaw/opencraft/internal/foundation/interact"
)

// approvalsFile is the on-disk shape of the workspace-owned
// approvals.yaml under ~/.opencraft/workspaces/<wid>/:
// dynamically approved commands, stored per project so they can be
// committed and shared with the team.
type approvalsFile struct {
	Version string   `json:"version"`
	Allow   []string `json:"allow,omitempty"`
}

// approvalsVersion is the current approvals file schema version.
const approvalsVersion = "v1"

// Manager owns the dynamic command allowlist and its project-backed
// approvals file. It is safe for concurrent use while Exec calls are
// in flight.
type Manager struct {
	allowlist *sandbox.Allowlist
	path      string
	mu        sync.Mutex
	hooks     *hooks.Manager
}

// New loads static rules plus the project approvals file (when it
// exists) into one allowlist. Invalid rules abort construction.
func New(rules []string, approvalsPath string) (*Manager, error) {
	a, err := sandbox.NewAllowlist(rules...)
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft execpolicy: static allowlist: %v", err)
	}
	m := &Manager{allowlist: a, path: approvalsPath}
	entries, err := m.readFile()
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		if err := a.Add(entries...); err != nil {
			return nil, err
		}
	}
	return m, nil
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
	opts, _ := json.Marshal([]interact.Option{
		{Label: "Allow once", Value: "allow_once"},
		{Label: "Deny", Value: "deny"},
		{Label: "Always allow", Value: "always"},
	})
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

// AlwaysAllow adds a rule to the allowlist and persists it to the
// project approvals file.
func (m *Manager) AlwaysAllow(rule string) error {
	if err := m.allowlist.Add(rule); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := m.readFile()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e == rule {
			return nil
		}
	}
	entries = append(entries, rule)
	return m.writeFile(entries)
}

// Remove deletes a rule from the allowlist and persists the change.
// It is a no-op when the rule is not present.
func (m *Manager) Remove(rule string) error {
	rules := m.Rules()
	filtered := rules[:0:0]
	removed := false
	for _, r := range rules {
		if r == rule {
			removed = true
			continue
		}
		filtered = append(filtered, r)
	}
	if !removed {
		return nil
	}
	if err := m.allowlist.Set(filtered); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := m.readFile()
	if err != nil {
		return err
	}
	out := entries[:0:0]
	for _, e := range entries {
		if e != rule {
			out = append(out, e)
		}
	}
	return m.writeFile(out)
}

// NormaliseCommand renders the normalized token list of an ExecRequest
// as the allowlist rule string ("sh -c" wrappers are unwrapped).
func NormaliseCommand(req sandbox.ExecRequest) string {
	return strings.Join(sandbox.NormaliseExec(req), " ")
}

func (m *Manager) readFile() ([]string, error) {
	if m.path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f approvalsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, errdefs.Validationf(
			"opencraft execpolicy: parse %s: %v", m.path, err)
	}
	return f.Allow, nil
}

func (m *Manager) writeFile(entries []string) error {
	if m.path == "" {
		return nil
	}
	data, err := yaml.Marshal(approvalsFile{
		Version: approvalsVersion,
		Allow:   entries,
	})
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".approvals-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, m.path)
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
	mgr, err := New(settings.AllowedCommands, settings.ApprovalsPath)
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
