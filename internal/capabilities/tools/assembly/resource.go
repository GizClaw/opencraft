// Package assembly owns opencraft's tool.Assembly/opencraft factory:
// flowcraft's middleware settings (recover / telemetry / timeout /
// concurrency / result_limit / truncate) plus the opencraft-specific
// middlewares — redact (secret stripping), audit (append-only, redacted
// tool-call trail), the delegation policy and the lifecycle hooks. The
// middleware implementations themselves all live upstream; what stays
// here is the chain order a deployment's settings select.
package assembly

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/hooks"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	toolmiddleware "github.com/GizClaw/flowcraft/core/tool/middleware"
	"go.opentelemetry.io/otel/attribute"

	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
)

// AssemblyImpl is the deploy impl id of opencraft's tool assembly.
const AssemblyImpl = "opencraft"

// AssemblySettings mirrors the core middleware assembly settings plus
// the opencraft-specific middleware sections.
type AssemblySettings struct {
	Middlewares *middlewareSettings `json:"middlewares,omitempty"`
	Dynamic     *tool.Policy        `json:"dynamic,omitempty"`
}

type middlewareSettings struct {
	// Settings is flowcraft's own middleware subtree: recover, telemetry,
	// timeout, concurrency, result_limit, truncate and the non-text part
	// budget, with the keys and the semantics its package documents.
	toolmiddleware.Settings
	// Redact and Audit are opencraft's additions to the chain.
	Redact *RedactSettings `json:"redact,omitempty"`
	Audit  *AuditSettings  `json:"audit,omitempty"`
}

// AssemblyFactory builds tool.Assembly/opencraft.
type AssemblyFactory struct{}

var _ resource.Factory = AssemblyFactory{}

// Spec implements resource.Factory.
func (AssemblyFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: tool.AssemblyKind,
		Impl: AssemblyImpl,
		Deps: []resource.DepSpec{{
			Name: "tool", Type: "tool.Source", Required: true, Many: true,
		}, {
			Name: "hooks", Type: hooks.ResourceKind, Required: false,
		}, {
			// Optional: the curated delegation target policy, enforced
			// on delegate calls by a middleware of this chain.
			Name: "policy", Type: subagents.PolicyResourceKind, Required: false,
		}},
	}
}

// New implements resource.Factory. The middleware chain is built
// outermost first; result post-processing therefore flows in reverse:
// the raw tool result passes redact → result_limit → truncate, and the
// audit middleware (outermost of the transforms) records the final
// content the model actually sees. Redact sits innermost so the
// persisted truncate cache never carries secrets either.
func (AssemblyFactory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[AssemblySettings](
		ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"tool middleware: decode assembly settings: %v", err)
	}

	values := in.DepsMany("tool")
	sources := make([]tool.Source, 0, len(values))
	for _, value := range values {
		src, ok := value.(tool.Source)
		if !ok {
			return nil, errdefs.Validationf(
				"tool middleware: assembly dep is %T, want tool.Source", value)
		}
		sources = append(sources, src)
	}
	if len(sources) == 0 {
		return nil, errdefs.Validationf(
			"tool middleware: assembly requires at least one tool source")
	}

	var hookMgr *hooks.Manager
	if dep, ok := in.Dep("hooks"); ok {
		if mgr, ok := dep.(*hooks.Manager); ok {
			hookMgr = mgr
		}
	}
	var policy *subagents.Policy
	if dep, ok := in.Dep("policy"); ok {
		if p, ok := dep.(*subagents.Policy); ok {
			policy = p
		}
	}
	mws, err := buildMiddleware(settings.Middlewares, hookMgr, policy)
	if err != nil {
		return nil, err
	}
	opts := []tool.AssemblyOption{tool.WithMiddleware(mws...)}
	if settings.Dynamic != nil {
		opts = append(opts, tool.WithDynamic(*settings.Dynamic))
	}
	return tool.NewAssembly(sources, opts...)
}

// buildMiddleware assembles the configured chain. A nil section (or a
// disabled one) is skipped; invalid settings surface as validation
// errors instead of silently degrading.
func buildMiddleware(
	s *middlewareSettings,
	hookMgr *hooks.Manager,
	policy *subagents.Policy,
) ([]tool.Middleware, error) {
	var mws []tool.Middleware
	core := toolmiddleware.Settings{}
	if s != nil {
		core.Recover = s.Recover
		core.Timeout = s.Timeout
		core.Concurrency = s.Concurrency
		// Non-text result parts are bounded even when the text limit is
		// off: a tool result rides every later turn's context, and media
		// has no natural size. flowcraft applies its own default (1 MiB)
		// when this is nil. The result_limit section wins when it names
		// a budget, because that limiter meters text and media together.
		partBudget := s.ResultPartBudgetBytes
		if s.ResultLimit != nil && s.ResultLimit.PartBudgetBytes != nil {
			partBudget = s.ResultLimit.PartBudgetBytes
		}
		core.ResultPartBudgetBytes = partBudget
	}
	built, err := toolmiddleware.FromSettings(core)
	if err != nil {
		return nil, err
	}
	mws = append(mws, built...)
	// The delegation policy sits behind the core middleware: a refused
	// delegate call still passes recover/timeout/metrics, so one
	// refusal looks exactly like any other rejected tool call.
	if mw := delegationMiddleware(policy); mw != nil {
		mws = append(mws, mw)
	}
	if s == nil {
		return mws, nil
	}

	// Redaction rules are compiled once and shared: they drive both the
	// model-facing middleware and the audit sink's redacted copies, so
	// the audit trail is always stripped even when redact is disabled.
	var rules []toolmiddleware.RedactRule
	if s.Redact != nil {
		rules, err = compileRedactRules(s.Redact.Rules)
		if err != nil {
			return nil, err
		}
	}
	// Audit outermost: it records after every inner transform, so the
	// trail contains the final redacted/limited/truncated content.
	if s.Audit != nil && s.Audit.Enabled {
		mw, err := auditMiddleware(s.Audit, rules)
		if err != nil {
			return nil, err
		}
		mws = append(mws, mw)
	}
	// The hard ceiling sits inside the text policy: on the way out,
	// truncate persists the full output and bounds text first (keeping
	// JSON envelopes parseable), so result_limit normally only has to
	// enforce the non-text part budget. Its text ceiling still applies
	// when a deployment leaves truncate off. Both middlewares are
	// flowcraft's; the settings are validated here because this chain
	// composes them itself instead of handing the whole subtree to
	// [toolmiddleware.FromSettings].
	if s.ResultLimit != nil {
		if s.ResultLimit.Max <= 0 {
			return nil, errdefs.Validationf(
				"tool middleware: result_limit.max must be positive, got %d",
				s.ResultLimit.Max)
		}
		opts := make([]toolmiddleware.ResultLimitOption, 0, 2)
		if s.ResultLimit.Marker != "" {
			opts = append(opts,
				toolmiddleware.WithResultMarker(s.ResultLimit.Marker))
		}
		if s.ResultLimit.PartBudgetBytes != nil {
			opts = append(opts, toolmiddleware.WithResultPartBudget(
				*s.ResultLimit.PartBudgetBytes))
		}
		mws = append(mws, toolmiddleware.ResultLimiter(s.ResultLimit.Max, opts...))
	}
	if s.Truncate != nil && s.Truncate.Enabled {
		if s.Truncate.MaxChars <= 0 {
			return nil, errdefs.Validationf(
				"tool middleware: truncate.max_chars must be positive, got %d",
				s.Truncate.MaxChars)
		}
		if strings.TrimSpace(s.Truncate.Dir) == "" {
			return nil, errdefs.Validationf(
				"tool middleware: truncate.dir is required")
		}
		if mw := toolmiddleware.Truncate(*s.Truncate); mw != nil {
			mws = append(mws, mw)
		}
	}
	if s.Redact != nil && s.Redact.Enabled && len(rules) > 0 {
		mws = append(mws, toolmiddleware.Redact(rules...))
	}
	// External lifecycle hooks run innermost: PreToolUse fires right
	// before the tool, PostToolUse sees the raw result.
	if mw := hooksMiddleware(hookMgr); mw != nil {
		mws = append(mws, mw)
	}
	return mws, nil
}

// hooksMiddleware fires PreToolUse before and PostToolUse after every
// tool call with a JSON event payload.
func hooksMiddleware(m *hooks.Manager) tool.Middleware {
	if m == nil || m.Empty() {
		return nil
	}
	return func(next tool.Dispatch) tool.Dispatch {
		return func(ctx context.Context, call message.ToolCall) message.ToolResult {
			args := map[string]any{}
			if len(call.Arguments) > 0 {
				if err := json.Unmarshal(call.Arguments, &args); err != nil {
					telemetry.WarnErr(ctx,
						"tool assembly: decode hook event arguments failed", err,
						attribute.String("tool.name", call.Name))
				}
			}
			m.Fire(ctx, hooks.EventPreToolUse, map[string]any{
				"event":      hooks.EventPreToolUse,
				"tool":       call.Name,
				"tool_input": args,
			})
			res := next(ctx, call)
			m.Fire(ctx, hooks.EventPostToolUse, map[string]any{
				"event":      hooks.EventPostToolUse,
				"tool":       call.Name,
				"tool_input": args,
				"tool_result": map[string]any{
					"content":  res.Content,
					"is_error": res.IsError,
				},
			})
			return res
		}
	}
}
