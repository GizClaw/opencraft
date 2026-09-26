package hooks

import (
	"context"

	"github.com/GizClaw/flowcraft/core/delegation/kanban"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/event"

	"github.com/GizClaw/flowcraft/core/resource"
)

// Factory builds the opencraft.hooks resource.
type Factory struct{}

var _ resource.Factory = Factory{}

// pluginHooksProvider is implemented by the shared plugin host
// (internal/capabilities/plugins/agent) and contributes plugin hook files.
type pluginHooksProvider interface {
	PluginHooks() []ExtraSource
}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ResourceKind,
		Impl: ResourceImpl,
		Deps: []resource.DepSpec{
			{Name: "plugin.host", Type: "opencraft.plugins", Required: false},
		},
	}
}

// New implements resource.Factory. A missing hooks.json yields an empty
// (no-op) manager, not an error.
func (Factory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[Settings](
		ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft hooks: decode settings: %v", err)
	}
	var extra []ExtraSource
	if dep, ok := in.Dep("plugin.host"); ok {
		if p, ok := dep.(pluginHooksProvider); ok && p != nil {
			extra = append(extra, p.PluginHooks()...)
		}
	}
	return LoadWithSources(ctx, settings.Path, extra)
}

// ObserverFactory builds the opencraft.hooks.observer resource.
type ObserverFactory struct{}

var _ resource.Factory = ObserverFactory{}

// Spec implements resource.Factory.
func (ObserverFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: ObserverResourceKind,
		Impl: "local",
		Deps: []resource.DepSpec{
			{Name: "events", Type: "event.Bus", Required: true},
			{Name: "hooks", Type: ResourceKind, Required: true},
		},
	}
}

// New implements resource.Factory.
func (ObserverFactory) New(
	_ context.Context,
	in resource.Input,
) (any, error) {
	busValue, ok := in.Dep("events")
	if !ok {
		return nil, errdefs.Validationf(
			"opencraft hooks observer: events dependency is required")
	}
	bus, ok := busValue.(event.Bus)
	if !ok {
		return nil, errdefs.Validationf(
			"opencraft hooks observer: events dep is %T, want event.Bus", busValue)
	}
	mgrValue, ok := in.Dep("hooks")
	if !ok {
		return nil, errdefs.Validationf(
			"opencraft hooks observer: hooks dependency is required")
	}
	mgr, ok := mgrValue.(*Manager)
	if !ok || mgr == nil {
		return nil, errdefs.Validationf(
			"opencraft hooks observer: hooks dep is not *hooks.Manager")
	}

	// The observer lives for the whole runtime generation; it owns its
	// loop context and Close cancels it.
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := bus.Subscribe(ctx, kanban.PatternAll())
	if err != nil {
		cancel()
		return nil, errdefs.Validationf(
			"opencraft hooks observer: subscribe: %v", err)
	}
	o := &Observer{mgr: mgr, sub: sub, cancel: cancel}
	go o.loop(ctx, sub.C())
	return o, nil
}
