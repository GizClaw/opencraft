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
	"sync"
	"time"

	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/hooks"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	toolmiddleware "github.com/GizClaw/flowcraft/core/tool/middleware"
	"go.opentelemetry.io/otel/attribute"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
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

var _ resource.Factory[*tool.Assembly] = AssemblyFactory{}

var (
	// depSources is every tool source in the deployment.
	depSources = resource.RequireMany[tool.Source]("tool")
	depHooks   = resource.Optional[*hooks.Manager]("hooks")
	// Optional: the curated delegation target policy, enforced on
	// delegate calls by a middleware of this chain.
	depPolicy = resource.Optional[*subagents.Policy]("policy")
	// Optional: the workspace session store the delegate tool's caller
	// mode is resolved from. Without it a delegation carries no
	// permission mode.
	depAssemblySessions = resource.Optional[*sessions.Store]("sessions")
)

// Spec implements resource.Factory.
func (AssemblyFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: tool.AssemblyKind,
		Impl: AssemblyImpl,
		Deps: []resource.Requirement{
			depSources.Spec(), depHooks.Spec(),
			depPolicy.Spec(), depAssemblySessions.Spec(),
		},
	}
}

// New implements resource.Factory. The middleware chain is built
// outermost first; result post-processing therefore flows in reverse:
// the raw tool result passes redact → result_limit → truncate, and the
// audit middleware (outermost of the transforms) records the final
// content the model actually sees. Redact sits innermost so the
// persisted truncate cache never carries secrets either.
func (AssemblyFactory) New(ctx context.Context, in resource.Input) (*tool.Assembly, error) {
	settings, err := resource.DecodeTyped[AssemblySettings](
		ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"tool middleware: decode assembly settings: %v", err)
	}

	sources, err := depSources.GetAll(in)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, errdefs.Validationf(
			"tool middleware: assembly requires at least one tool source")
	}

	hookMgr, _, err := depHooks.TryGet(in)
	if err != nil {
		return nil, err
	}
	policy, _, err := depPolicy.TryGet(in)
	if err != nil {
		return nil, err
	}
	store, _, err := depAssemblySessions.TryGet(in)
	if err != nil {
		return nil, err
	}
	mws, catTimeout, err := buildMiddleware(
		settings.Middlewares, hookMgr, policy, store)
	if err != nil {
		return nil, err
	}
	if catTimeout != nil {
		// The timeout middleware resolves every tool's SelfTimeout
		// claim through the assembly's registry, which tool.NewAssembly
		// only builds later; this source is how the registry reaches it
		// (see catalogTimeout).
		sources = append(sources, timeoutCatalogSource{timeout: catTimeout})
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
	store *sessions.Store,
) ([]tool.Middleware, *catalogTimeout, error) {
	var mws []tool.Middleware
	core := toolmiddleware.Settings{}
	var timeoutSettings *toolmiddleware.TimeoutSettings
	if s != nil {
		core.Recover = s.Recover
		// The timeout section is not handed to FromSettings: its
		// middleware needs the assembly's own registry to resolve each
		// tool's ToolMeta.SelfTimeout claim (see catalogTimeout). It is
		// added below in the position FromSettings would have used —
		// after the result policy, just outside the concurrency limiter.
		timeoutSettings = s.Timeout
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
		return nil, nil, err
	}
	mws = append(mws, built...)
	var timeout *catalogTimeout
	if timeoutSettings != nil && timeoutSettings.Default != "" {
		delay, err := time.ParseDuration(timeoutSettings.Default)
		if err != nil {
			return nil, nil, errdefs.Validationf(
				"tool middleware: timeout.default: %v", err)
		}
		timeout = newCatalogTimeout(delay, perToolTimeoutExemptions())
		mw := timeout.middleware()
		// FromSettings appends the concurrency limiter last when it is
		// enabled, so inserting before the tail keeps the canonical
		// order: the timeout sits outside the semaphore.
		if s.Concurrency != nil && s.Concurrency.Limit > 0 {
			mws = append(mws[:len(mws)-1], mw, mws[len(mws)-1])
		} else {
			mws = append(mws, mw)
		}
	}
	// The delegation policy sits behind the core middleware: a refused
	// delegate call still passes recover/timeout/metrics, so one
	// refusal looks exactly like any other rejected tool call.
	if mw := delegationMiddleware(policy); mw != nil {
		mws = append(mws, mw)
	}
	// The caller's permission mode rides the call as a run attribute, and
	// the delegation service turns it into the subagent session's own
	// mode. Inner to the policy: a refused call never gets that far.
	if mw := delegationModeMiddleware(store); mw != nil {
		mws = append(mws, mw)
	}
	if s == nil {
		return mws, timeout, nil
	}

	// Redaction rules are compiled once and shared: they drive both the
	// model-facing middleware and the audit sink's redacted copies, so
	// the audit trail is always stripped even when redact is disabled.
	var rules []toolmiddleware.RedactRule
	if s.Redact != nil {
		rules, err = compileRedactRules(s.Redact.Rules)
		if err != nil {
			return nil, nil, err
		}
	}
	// Audit outermost: it records after every inner transform, so the
	// trail contains the final redacted/limited/truncated content.
	if s.Audit != nil && s.Audit.Enabled {
		mw, err := auditMiddleware(s.Audit, rules)
		if err != nil {
			return nil, nil, err
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
			return nil, nil, errdefs.Validationf(
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
		limiter, err := toolmiddleware.ResultLimiter(s.ResultLimit.Max, opts...)
		if err != nil {
			return nil, nil, err
		}
		mws = append(mws, limiter)
	}
	if s.Truncate != nil && s.Truncate.Enabled {
		if s.Truncate.MaxChars <= 0 {
			return nil, nil, errdefs.Validationf(
				"tool middleware: truncate.max_chars must be positive, got %d",
				s.Truncate.MaxChars)
		}
		if strings.TrimSpace(s.Truncate.Dir) == "" {
			return nil, nil, errdefs.Validationf(
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
	return mws, timeout, nil
}

// catalogTimeout builds the timeout middleware once the assembly's
// registry exists. toolmiddleware.TimeoutWithCatalog resolves each
// tool's ToolMeta.SelfTimeout claim through a Catalog, and a tool that
// bounds itself (exec_command and exec_session manage their own
// deadlines; the image, video, search and plugin tools own transport
// or provider timeouts) must not be wrapped in a competing one. The
// registry is constructed inside tool.NewAssembly, after this chain is
// assembled, so the middleware resolves it at composition time and the
// registry arrives through timeoutCatalogSource.Attach.
type catalogTimeout struct {
	delay   time.Duration
	perTool map[string]time.Duration

	mu      sync.Mutex
	catalog tool.Catalog
}

func newCatalogTimeout(
	delay time.Duration, perTool map[string]time.Duration,
) *catalogTimeout {
	return &catalogTimeout{delay: delay, perTool: perTool}
}

// attach hands over the assembly's registry. The registrar NewAssembly
// gives to attachers is the registry itself (tool.Registry implements
// both roles), so the read side is recovered from it here.
func (c *catalogTimeout) attach(registrar tool.Registrar) {
	catalog, ok := registrar.(tool.Catalog)
	if !ok {
		telemetry.Warn(context.Background(),
			"tool assembly: registry is not a catalog; SelfTimeout claims are ignored")
		return
	}
	c.mu.Lock()
	c.catalog = catalog
	c.mu.Unlock()
}

// middleware returns the chain entry. NewExecutor composes the chain
// once, after the attachers ran, so resolution here is synchronous; an
// assembly composed without the attacher falls back to the catalog-less
// timeout, which honours perTool but not SelfTimeout claims.
func (c *catalogTimeout) middleware() tool.Middleware {
	return func(next tool.Dispatch) tool.Dispatch {
		c.mu.Lock()
		catalog := c.catalog
		c.mu.Unlock()
		if catalog == nil {
			return toolmiddleware.Timeout(c.delay, c.perTool)(next)
		}
		return toolmiddleware.TimeoutWithCatalog(catalog, c.delay, c.perTool)(next)
	}
}

// perToolTimeoutExemptions names tools whose calls may outlive the
// generic default and whose definitions this repository does not own,
// so they cannot declare ToolMeta.SelfTimeout themselves. A zero limit
// exempts the name; a perTool entry wins over the tool's own claim, so
// host policy stays authoritative.
//
// The delegate tool is one call per delegation: sync mode blocks the
// caller's turn until the delegated worker reaches a terminal state,
// so the generic default would cut every long delegation short and
// turn its result into a timeout error. The worker's own execution and
// the run's stop are the bounds.
func perToolTimeoutExemptions() map[string]time.Duration {
	return map[string]time.Duration{sdkdelegation.ToolName: 0}
}

// timeoutCatalogSource publishes no tools: it exists so NewAssembly's
// attacher pass can hand the built registry to the timeout middleware.
type timeoutCatalogSource struct {
	timeout *catalogTimeout
}

func (timeoutCatalogSource) Tools() []tool.Tool         { return nil }
func (timeoutCatalogSource) LazyTools() []tool.LazyTool { return nil }

// Attach implements tool.RegistryAttacher.
func (s timeoutCatalogSource) Attach(r tool.Registrar) { s.timeout.attach(r) }

var (
	_ tool.Source           = timeoutCatalogSource{}
	_ tool.RegistryAttacher = timeoutCatalogSource{}
)

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
