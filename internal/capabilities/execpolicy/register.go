package execpolicy

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
)

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
		Kind: ResourceKind,
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
