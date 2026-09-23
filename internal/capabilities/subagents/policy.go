package subagents

import (
	"context"
	"path"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// PolicyResourceKind is the deploy resource kind of the delegation
// policy: the curated list of targets the model may delegate to.
const PolicyResourceKind = "opencraft.delegation.policy"

// PolicySettings is the policy's settings shape. It is the config
// package's shape, so the settings page and the runtime read and write
// one definition.
type PolicySettings = config.DelegationPolicySettings

// Policy is the resolved target policy. The zero value allows
// everything, which is what a deployment without the resource gets: an
// installation that never curated a list is not silently locked down.
type Policy struct {
	allowed []string
	blocked []string
}

// NewPolicy builds a policy from target patterns. Patterns are matched
// with path.Match semantics over the whole name, so "researcher*"
// covers a family of targets while a bare name covers exactly one.
func NewPolicy(allowed, blocked []string) *Policy {
	return &Policy{
		allowed: config.NormalizeTargetPatterns(allowed),
		blocked: config.NormalizeTargetPatterns(blocked),
	}
}

// Empty reports whether the policy restricts nothing.
func (p *Policy) Empty() bool {
	return p == nil || (len(p.allowed) == 0 && len(p.blocked) == 0)
}

// Allows reports whether a target may be delegated to. Blocking wins
// over allowing, and an allowlist is exhaustive: once it is non-empty,
// every target not named by it is refused.
func (p *Policy) Allows(target string) bool {
	name := strings.TrimSpace(target)
	if name == "" {
		return false
	}
	if p == nil {
		return true
	}
	for _, pattern := range p.blocked {
		if matchesTarget(pattern, name) {
			return false
		}
	}
	if len(p.allowed) == 0 {
		return true
	}
	for _, pattern := range p.allowed {
		if matchesTarget(pattern, name) {
			return true
		}
	}
	return false
}

// Allowed returns the configured allow patterns.
func (p *Policy) Allowed() []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), p.allowed...)
}

// Blocked returns the configured block patterns.
func (p *Policy) Blocked() []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), p.blocked...)
}

// Describe renders the policy for an error message the model reads: a
// refusal has to say what would have been allowed, or the next attempt
// is a guess.
func (p *Policy) Describe() string {
	if p.Empty() {
		return "no target restriction is configured"
	}
	var parts []string
	if allowed := p.Allowed(); len(allowed) > 0 {
		parts = append(parts,
			"allowed targets: "+strings.Join(allowed, ", "))
	}
	if blocked := p.Blocked(); len(blocked) > 0 {
		parts = append(parts,
			"blocked targets: "+strings.Join(blocked, ", "))
	}
	return strings.Join(parts, "; ")
}

func matchesTarget(pattern, name string) bool {
	if pattern == name {
		return true
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

// PolicyFactory builds the opencraft.delegation.policy resource.
type PolicyFactory struct{}

var _ resource.Factory = PolicyFactory{}

// Spec implements resource.Factory.
func (PolicyFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: PolicyResourceKind,
		Impl: "local",
	}
}

// New implements resource.Factory. A malformed pattern is a settings
// error rather than a silently ignored entry: a policy that does not
// do what it says is worse than no policy.
func (PolicyFactory) New(
	ctx context.Context, in resource.Input,
) (any, error) {
	settings, err := resource.DecodeTyped[PolicySettings](ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"delegation policy resource: decode settings: %v", err)
	}
	if err := config.ValidateTargetPatterns(
		settings.AllowedTargets, settings.BlockedTargets,
	); err != nil {
		return nil, errdefs.Validationf(
			"delegation policy resource: %v", err)
	}
	return NewPolicy(settings.AllowedTargets, settings.BlockedTargets), nil
}

// Contract names the policy's external dependency contract, for
// assemblies that receive the policy from the caller instead of the
// document.
const Contract = "opencraft.delegation_policy"
