// Sandbox exec policy: layered static rules, a project-scoped approvals
// file, and an approver that asks the user through the core prompt
// protocol.
package app

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
	"github.com/GizClaw/flowcraft/core/sandbox"
	"sigs.k8s.io/yaml"

	"github.com/GizClaw/opencraft/internal/interact"
	"github.com/GizClaw/opencraft/internal/tools/requestpermissions"
	"github.com/GizClaw/opencraft/internal/app/worldstate"
)

// approvalsFile is the on-disk shape of .opencraft/approvals.yaml:
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

// Approve implements sandbox.ApprovalFunc: it asks the user through
// the core prompt protocol and grows the allowlist when the user
// chooses to always allow the command. Ask failures are fail-closed.
func (m *Manager) Approve(
	ctx context.Context,
	req sandbox.ApprovalRequest,
) (sandbox.Decision, error) {
	command := NormaliseCommand(req.Exec)
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

// NormaliseCommand renders the normalized token list of an ExecRequest
// as the allowlist rule string ("sh -c" wrappers are unwrapped).
func NormaliseCommand(req sandbox.ExecRequest) string {
	return strings.Join(sandbox.NormaliseExec(req), " ")
}

func (m *Manager) readFile() ([]string, error) {
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
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
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
// Host capability: expose the policy manager to tools.
// ---------------------------------------------------------------------------

type execPolicyHost struct {
	agent.Host
	policy requestpermissions.Policy
}

// ExecPolicy implements requestpermissions.PolicyProvider.
func (h execPolicyHost) ExecPolicy() requestpermissions.Policy { return h.policy }

// UnwrapHost preserves optional capabilities of the inner host.
func (h execPolicyHost) UnwrapHost() agent.Host { return h.Host }

// WithExecPolicy wraps h with the exec policy capability. Install it
// before host middleware so built-in decorators preserve access.
func WithExecPolicy(h agent.Host, policy requestpermissions.Policy) agent.Host {
	if h == nil || policy == nil {
		panic("opencraft execpolicy: WithExecPolicy requires a host and policy")
	}
	return execPolicyHost{Host: h, policy: policy}
}

// ExecPolicyFromHost returns the exec policy exposed by h, if any.
func ExecPolicyFromHost(h agent.Host) (requestpermissions.Policy, bool) {
	provider, ok := agent.CapabilityFromHost[requestpermissions.PolicyProvider](h)
	if !ok || provider.ExecPolicy() == nil {
		return nil, false
	}
	return provider.ExecPolicy(), true
}

// policyHolder captures the policy manager built by the sandbox
// factory so the runtime can wrap it into every turn host.
type policyHolder struct {
	mu     sync.Mutex
	policy requestpermissions.Policy
}

func (h *policyHolder) set(policy requestpermissions.Policy) {
	h.mu.Lock()
	h.policy = policy
	h.mu.Unlock()
}

func (h *policyHolder) get() requestpermissions.Policy {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.policy
}

// Rules implements worldstate.PrefixProvider on the holder so the
// permissions section can read the live allowlist without racing the
// sandbox factory that fills it during build.
func (h *policyHolder) Rules() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.policy == nil {
		return nil
	}
	return h.policy.Rules()
}

var _ requestpermissions.PolicyProvider = execPolicyHost{}
var _ requestpermissions.Policy = (*Manager)(nil)
var _ worldstate.PrefixProvider = (*policyHolder)(nil)
