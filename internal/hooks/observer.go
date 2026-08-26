package hooks

import (
	"context"

	"github.com/GizClaw/flowcraft/core/delegation/kanban"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/flowcraft/core/resource"
)

// ObserverResourceKind is the deploy resource kind of the subagent
// hook observer.
const ObserverResourceKind = "opencraft.hooks.observer"

// Observer subscribes to delegation kanban events and fires
// SubagentStart / SubagentStop hooks.
type Observer struct {
	mgr    *Manager
	sub    event.Subscription
	cancel context.CancelFunc
}

// Close unsubscribes and stops the observer loop.
func (o *Observer) Close() error {
	o.cancel()
	return o.sub.Close()
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

func (o *Observer) loop(ctx context.Context, ch <-chan event.Envelope) {
	for {
		select {
		case env, ok := <-ch:
			if !ok {
				return
			}
			var ev kanban.CardEvent
			if err := env.Decode(&ev); err != nil {
				telemetry.Warn(ctx, "opencraft hooks observer: decode card event failed",
					otellog.String("subject", string(env.Subject)),
					otellog.String("error", err.Error()))
				continue
			}
			switch ev.Status {
			case kanban.StatusClaimed:
				o.fire(ctx, EventSubagentStart, ev)
			case kanban.StatusDone, kanban.StatusFailed, kanban.StatusCanceled:
				o.fire(ctx, EventSubagentStop, ev)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (o *Observer) fire(ctx context.Context, event string, ev kanban.CardEvent) {
	payload := map[string]any{
		"event":    event,
		"subagent": ev.Consumer,
		"card_id":  ev.CardID,
		"run_id":   ev.RunID,
		"status":   string(ev.Status),
	}
	if ev.Request != nil {
		payload["target"] = ev.Request.Request.Target
		payload["message"] = ev.Request.Request.Input
	}
	o.mgr.Fire(ctx, event, payload)
}
