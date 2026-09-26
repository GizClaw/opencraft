package review

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"

	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

// Factory builds the opencraft.review observe hook. Every dependency is
// optional except the queue: a runtime without a user database, a
// router or a skills registry still assembles, it simply cannot review.
type Factory struct{}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.observe",
		Impl: ResourceImpl,
		Deps: []resource.DepSpec{
			{Name: "queue", Type: reviewstore.ResourceKind, Required: true},
			{Name: "memory", Type: userstore.ResourceKind, Required: false},
			{Name: "skills", Type: skills.ResourceKind, Required: false},
			{Name: "sessions", Type: ocsessions.ResourceKind, Required: false},
			{Name: "router", Type: "inference.Router", Required: false},
			{Name: "observer", Type: opmemory.UsageObserverResourceKind, Required: false},
		},
	}
}

// New implements resource.Factory.
func (Factory) New(ctx context.Context, in resource.Input) (any, error) {
	queue, err := resourcedep.Required[*reviewstore.Binding](
		in, "review", "queue")
	if err != nil {
		return nil, err
	}
	settings, err := resource.DecodeTyped[Settings](ctx, in.Settings)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(settings.WorkDir) == "" {
		telemetry.Warn(ctx,
			"review: hook assembled without work_dir; "+
				"workspace-scoped candidates will be rejected")
	}
	o := &Observer{
		queue:    queue,
		settings: settings,
		turns:    map[string]int{},
		inflight: map[string]bool{},
	}
	if dep, ok := in.Dep("memory"); ok {
		if binding, ok := dep.(*userstore.Binding); ok {
			o.memory = binding
		}
	}
	if dep, ok := in.Dep("skills"); ok {
		if svc, ok := dep.(*skills.Service); ok {
			o.skills = svc
		}
	}
	if dep, ok := in.Dep("sessions"); ok {
		if store, ok := dep.(*ocsessions.Store); ok {
			o.sessions = store
		}
	}
	if dep, ok := in.Dep("router"); ok {
		if router, ok := dep.(*route.Router); ok {
			o.router = router
		}
	}
	if dep, ok := in.Dep("observer"); ok {
		if observer, ok := dep.(opmemory.UsageObserver); ok {
			o.usage = observer
		}
	}
	return o, nil
}
