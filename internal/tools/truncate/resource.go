package truncate

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/tool"
	toolmiddleware "github.com/GizClaw/flowcraft/core/tool/middleware"
)

// AssemblyImpl is the deploy impl id of opencraft's tool assembly. It
// wraps the core middleware chain with the result-truncation
// middleware, so oversized tool outputs are persisted and shortened
// before they reach the model context.
const AssemblyImpl = "opencraft"

// AssemblySettings mirrors the core middleware assembly settings plus
// the truncate section.
type AssemblySettings struct {
	Middlewares *middlewareSettings `json:"middlewares,omitempty"`
	Dynamic     *tool.Policy        `json:"dynamic,omitempty"`
}

type middlewareSettings struct {
	Recover     *toolmiddleware.RecoverSettings     `json:"recover,omitempty"`
	Timeout     *toolmiddleware.TimeoutSettings     `json:"timeout,omitempty"`
	Concurrency *toolmiddleware.ConcurrencySettings `json:"concurrency,omitempty"`
	Truncate    *Settings                           `json:"truncate,omitempty"`
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
		}},
	}
}

// New implements resource.Factory.
func (AssemblyFactory) New(_ context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[AssemblySettings](
		in.Settings, resource.ExpandEnv())
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

	core := toolmiddleware.Settings{}
	if settings.Middlewares != nil {
		core.Recover = settings.Middlewares.Recover
		core.Timeout = settings.Middlewares.Timeout
		core.Concurrency = settings.Middlewares.Concurrency
	}
	mws, err := toolmiddleware.FromSettings(core)
	if err != nil {
		return nil, err
	}
	if settings.Middlewares != nil && settings.Middlewares.Truncate != nil {
		if mw := Middleware(*settings.Middlewares.Truncate); mw != nil {
			mws = append(mws, mw)
		}
	}

	opts := []tool.AssemblyOption{tool.WithMiddleware(mws...)}
	if settings.Dynamic != nil {
		opts = append(opts, tool.WithDynamic(*settings.Dynamic))
	}
	return tool.NewAssembly(sources, opts...)
}
