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
)

// Factory builds the opencraft.review observe hook. Every dependency is
// optional except the queue: a runtime without a user database, a
// router or a skills registry still assembles, it simply cannot review.
type Factory struct{}

var _ resource.Factory[*Observer] = Factory{}

// The queue is the only dependency a review cannot do without: a runtime
// without a user database, a router or a skills registry still assembles,
// it simply cannot review.
var (
	depQueue    = resource.Require[*reviewstore.Binding]("queue")
	depMemory   = resource.Optional[*userstore.Binding]("memory")
	depSkills   = resource.Optional[*skills.Service]("skills")
	depSessions = resource.Optional[*ocsessions.Store]("sessions")
	depRouter   = resource.Optional[*route.Router]("router")
	depUsage    = resource.Optional[opmemory.UsageObserver]("observer")
)

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.observe",
		Impl: ResourceImpl,
		Deps: []resource.Requirement{
			depQueue.Spec(), depMemory.Spec(), depSkills.Spec(),
			depSessions.Spec(), depRouter.Spec(), depUsage.Spec(),
		},
	}
}

// New implements resource.Factory.
func (Factory) New(ctx context.Context, in resource.Input) (*Observer, error) {
	queue, err := depQueue.Get(in)
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
	optional := []struct {
		name string
		read func() (any, bool, error)
		set  func(any) error
	}{}
	_ = optional
	if binding, ok, err := depMemory.TryGet(in); err != nil {
		return nil, err
	} else if ok {
		o.memory = binding
	}
	if svc, ok, err := depSkills.TryGet(in); err != nil {
		return nil, err
	} else if ok {
		o.skills = svc
	}
	if store, ok, err := depSessions.TryGet(in); err != nil {
		return nil, err
	} else if ok {
		o.sessions = store
	}
	if router, ok, err := depRouter.TryGet(in); err != nil {
		return nil, err
	} else if ok {
		o.router = router
	}
	if observer, ok, err := depUsage.TryGet(in); err != nil {
		return nil, err
	} else if ok {
		o.usage = observer
	}
	return o, nil
}
