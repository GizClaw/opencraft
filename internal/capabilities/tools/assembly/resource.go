// Package assembly owns opencraft's tool.Assembly/opencraft factory:
// the core middleware chain (recover / timeout / concurrency) plus the
// result-quality and security middlewares — truncate (persisted
// excerpt), result_limit (hard ceiling), redact (secret stripping)
// and audit (append-only, redacted tool-call trail).
package assembly

import (
	"context"
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	toolmiddleware "github.com/GizClaw/flowcraft/core/tool/middleware"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
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
	Recover     *toolmiddleware.RecoverSettings     `json:"recover,omitempty"`
	Timeout     *toolmiddleware.TimeoutSettings     `json:"timeout,omitempty"`
	Concurrency *toolmiddleware.ConcurrencySettings `json:"concurrency,omitempty"`
	Truncate    *TruncateSettings                   `json:"truncate,omitempty"`
	ResultLimit *ResultLimitSettings                `json:"result_limit,omitempty"`
	Redact      *RedactSettings                     `json:"redact,omitempty"`
	Audit       *AuditSettings                      `json:"audit,omitempty"`
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
	mws, err := buildMiddleware(settings.Middlewares, hookMgr)
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
) ([]tool.Middleware, error) {
	var mws []tool.Middleware
	core := toolmiddleware.Settings{}
	if s != nil {
		core.Recover = s.Recover
		core.Timeout = s.Timeout
		core.Concurrency = s.Concurrency
	}
	built, err := toolmiddleware.FromSettings(core)
	if err != nil {
		return nil, err
	}
	mws = append(mws, built...)
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
	if s.Truncate != nil {
		if mw := truncateMiddleware(*s.Truncate); mw != nil {
			mws = append(mws, mw)
		}
	}
	mw, err := resultLimitMiddleware(s.ResultLimit)
	if err != nil {
		return nil, err
	}
	if mw != nil {
		mws = append(mws, mw)
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
						otellog.String("tool.name", call.Name))
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
