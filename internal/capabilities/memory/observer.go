package memory

import (
	"context"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/resource"
)

// UsageObserverResourceKind is the optional resource kind through
// which engine wiring supplies a condensation usage reporter.
const UsageObserverResourceKind = "memory.UsageObserver"

// UsageObserver receives usage produced by memory condensation.
type UsageObserver interface {
	ReportUsage(context.Context, inference.Usage)
}

// UsageObserverFunc adapts a function to UsageObserver.
type UsageObserverFunc func(context.Context, inference.Usage)

// ReportUsage implements UsageObserver.
func (f UsageObserverFunc) ReportUsage(ctx context.Context, u inference.Usage) {
	f(ctx, u)
}

type usageObserverFactory struct {
	fn UsageObserverFunc
}

func (usageObserverFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: UsageObserverResourceKind,
		Impl: "function",
	}
}

func (f usageObserverFactory) New(_ context.Context, _ resource.Input) (any, error) {
	return f.fn, nil
}
