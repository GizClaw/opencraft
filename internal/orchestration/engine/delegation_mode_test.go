package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"
	"github.com/GizClaw/flowcraft/craft"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

// subagentDeployment is the deploy snapshot the local directory binds:
// one target, whose engine records the mode its own session resolved.
type subagentDeployment struct{ instance *agent.Agent }

func (d subagentDeployment) Agent(id string) (*agent.Agent, bool) {
	if d.instance == nil || id != d.instance.ID {
		return nil, false
	}
	return d.instance, true
}

func (d subagentDeployment) AgentNames() []string {
	if d.instance == nil {
		return nil
	}
	return []string{d.instance.ID}
}

// delegationProbe is one assembled delegation service plus the child
// sessions it ran, in call order.
type delegationProbe struct {
	service  *sdkdelegation.LocalService
	store    *ocsessions.Store
	childIDs []string
	// childModes is the child session's stored mode; nestedModes is what
	// a delegate call made from inside the child resolves through the
	// same helper the tool assembly uses — i.e. what a grandchild
	// inherits.
	childModes  []ocsessions.Mode
	nestedModes []ocsessions.Mode
	nestedFound []bool
}

// newDelegationProbe assembles the delegation service the way the
// runtime does — through the app's factory, with the workspace session
// store as a deploy dependency — and binds it to a real session manager
// whose subagent engine reads its own session's mode back from the
// store. That read is exactly what the sandbox and the tool policies do,
// so a mode that never landed on the child's session shows up here.
func newDelegationProbe(t *testing.T) *delegationProbe {
	t.Helper()
	store, err := sessionstore.Open(t, filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}

	probe := &delegationProbe{store: store}
	child := agent.EngineFunc(func(
		ctx context.Context,
		run agent.Run,
		_ agent.Host,
		board *agent.Board,
	) (*agent.Board, error) {
		mode, err := store.Mode(ctx, run.ConversationID)
		if err != nil {
			return board, err
		}
		probe.childIDs = append(probe.childIDs, run.ConversationID)
		probe.childModes = append(probe.childModes, mode)
		// A handler runs on a context re-stamped with the run's own
		// identity (core does this per graph node), and that is the
		// context a delegate call made from inside this session resolves
		// through — i.e. what a grandchild inherits.
		nested, found, err := ocsessions.ModeFromContext(
			agent.WithRunInfo(ctx, run.Info()), store)
		if err != nil {
			return board, err
		}
		probe.nestedModes = append(probe.nestedModes, nested)
		probe.nestedFound = append(probe.nestedFound, found)
		board.AppendChannelMessage(agent.MainChannel,
			message.NewTextMessage(message.RoleAssistant, "done"))
		return board, nil
	})
	resolver := agentResolverFunc(func(id string) (*agent.Agent, bool) {
		if id != "writer" {
			return nil, false
		}
		return &agent.Agent{ID: "writer", Engine: child}, true
	})
	router := event.NewRouter(event.NewMemoryBus())
	t.Cleanup(func() { _ = router.Close() })
	manager, err := coresession.NewManager(
		resolver,
		coresession.HostFactoryFunc(func(
			context.Context, coresession.HostRequest,
		) (agent.Host, error) {
			return agent.NoopHost{}, nil
		}),
		router,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	directory := sdkdelegation.NewDirectory()
	if err := directory.Bind(subagentDeployment{
		instance: &agent.Agent{ID: "writer"},
	}); err != nil {
		t.Fatal(err)
	}
	value, err := (delegationServiceFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{"max_concurrency": 2, "max_depth": 2}`),
		Deps: map[string]any{
			"directory":           directory,
			delegationSessionsDep: store,
		},
	})
	if err != nil {
		t.Fatalf("build delegation service: %v", err)
	}
	service, ok := value.(*sdkdelegation.LocalService)
	if !ok {
		t.Fatalf("delegation service = %T, want *delegation.LocalService", value)
	}
	t.Cleanup(func() { _ = service.Close() })
	if err := service.BindSessionManager(manager); err != nil {
		t.Fatal(err)
	}
	probe.service = service
	return probe
}

// delegate runs one synchronous delegation from the named caller
// session, with whatever run attributes the caller's middleware stamped.
func (p *delegationProbe) delegate(
	t *testing.T, callerID string, attributes map[string]string,
) sdkdelegation.Response {
	t.Helper()
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{
		Identity: agent.Identity{AgentID: "assistant", ConversationID: callerID},
	})
	response, err := p.service.Delegate(ctx, sdkdelegation.Request{
		Mode:       sdkdelegation.ModeSync,
		Target:     "writer",
		Input:      "write the changelog",
		Attributes: attributes,
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if response.Status != sdkdelegation.StatusSucceeded {
		t.Fatalf("delegation status = %q, want succeeded", response.Status)
	}
	return response
}

// TestDelegationAppliesCallerModeToSubagentSession is the regression for
// the reported bug: a yolo session delegated work, and the subagent ran
// confined because its own session had no mode, so every approval it
// asked for was refused on the caller's behalf. The mode now travels as
// a run attribute (stamped by the tool assembly, see
// delegationModeMiddleware) and is written to the child's session by the
// service the app assembles.
func TestDelegationAppliesCallerModeToSubagentSession(t *testing.T) {
	probe := newDelegationProbe(t)
	response := probe.delegate(t, "conv-caller", map[string]string{
		ocsessions.ModeAttribute: string(ocsessions.ModeYOLO),
	})

	if len(probe.childIDs) != 1 {
		t.Fatalf("the subagent ran %d times, want 1", len(probe.childIDs))
	}
	// The mode has to land on the session the subagent actually runs in,
	// not on some other id the initializer happened to see. Core writes
	// this metadata key; it is the only handle on that session.
	if got := response.Metadata["delegation.session_id"]; got != probe.childIDs[0] {
		t.Fatalf("delegation session id = %q, subagent ran in %q",
			got, probe.childIDs[0])
	}
	if got := probe.childModes[0]; got != ocsessions.ModeYOLO {
		t.Fatalf("subagent session mode = %q, want yolo", got)
	}
	// The doc comment on sessions.ModeInitializer promises a grandchild
	// inherits the same way: a delegate call made from inside this
	// session stamping this mode is the whole of that promise.
	if !probe.nestedFound[0] {
		t.Fatal("a delegate call from inside the subagent resolves no mode")
	}
	if got := probe.nestedModes[0]; got != ocsessions.ModeYOLO {
		t.Fatalf("mode a grandchild would inherit = %q, want yolo", got)
	}
}

// TestDelegationInheritsAConfinedCallerMode pins the other direction:
// the attribute is not a "widen to yolo" flag. A read-only caller's
// subagent starts read-only, and the store's workspace default would
// never produce that, so the value can only have come from the
// attribute. Which one the caller had is resolved by the tool assembly,
// see sessions.ModeFromContext.
func TestDelegationInheritsAConfinedCallerMode(t *testing.T) {
	skipIfYoloOnly(t)
	probe := newDelegationProbe(t)
	probe.delegate(t, "conv-caller", map[string]string{
		ocsessions.ModeAttribute: string(ocsessions.ModeReadOnly),
	})

	if len(probe.childModes) != 1 {
		t.Fatalf("the subagent ran %d times, want 1", len(probe.childModes))
	}
	if got := probe.childModes[0]; got != ocsessions.ModeReadOnly {
		t.Fatalf("subagent session mode = %q, want read-only", got)
	}
}

// TestDelegationWithoutAModeStillRuns keeps the degenerate delegation
// working: no attribute means the initializer writes nothing, so the
// child runs under whatever the store resolves on its own. That value is
// profile-dependent (workspace, or yolo in the yoloonly build) and is
// pinned where it is observable — the settings document is never
// written, see TestModeInitializerLeavesUndeclaredDelegationsAlone.
func TestDelegationWithoutAModeStillRuns(t *testing.T) {
	probe := newDelegationProbe(t)
	probe.delegate(t, "conv-caller", nil)

	if len(probe.childIDs) != 1 {
		t.Fatalf("the subagent ran %d times, want 1", len(probe.childIDs))
	}
}

// TestDelegationServiceRequiresTheSessionStore pins the assembly's
// fail-closed side: a deployment that cannot resolve the store must not
// silently build a service whose subagents never inherit a mode.
func TestDelegationServiceRequiresTheSessionStore(t *testing.T) {
	directory := sdkdelegation.NewDirectory()
	if err := directory.Bind(subagentDeployment{instance: &agent.Agent{ID: "writer"}}); err != nil {
		t.Fatal(err)
	}
	_, err := (delegationServiceFactory{}).New(context.Background(), resource.Input{
		Settings: []byte(`{"max_concurrency": 2}`),
		Deps:     map[string]any{"directory": directory},
	})
	if err == nil {
		t.Fatal("the factory built a delegation service without a session store")
	}
	// Name the dependency, so the failure cannot pass for an unrelated
	// one core's factory might grow.
	if !strings.Contains(err.Error(), delegationSessionsDep) {
		t.Fatalf("error %q does not name the %q dependency",
			err, delegationSessionsDep)
	}
}

// TestDelegationServiceSpecDeclaresTheSessionStore pins the factory
// side of the wiring: the dependency it requires is the one the shipped
// asset has to bind. The asset half is pinned by
// config.TestEmbeddedToolsWireDelegateMode.
func TestDelegationServiceSpecDeclaresTheSessionStore(t *testing.T) {
	spec := (delegationServiceFactory{}).Spec()
	for _, dep := range spec.Deps {
		if dep.Name != delegationSessionsDep {
			continue
		}
		if !dep.Required {
			t.Fatal("the session store dependency is optional")
		}
		if dep.Type != ocsessions.ResourceKind {
			t.Fatalf("session store dep type = %q, want %q",
				dep.Type, ocsessions.ResourceKind)
		}
		return
	}
	t.Fatalf("the delegation service spec has no %q dependency",
		delegationSessionsDep)
}

// skipIfYoloOnly stops the tests asserting a confined mode: the
// yoloonly profile pins every session to yolo, so there is nothing
// confined to inherit.
func skipIfYoloOnly(t *testing.T) {
	t.Helper()
	if profile.YoloOnly() {
		t.Skip("confined session modes do not exist in the yoloonly build")
	}
}

// TestDelegationCapabilityRegistersTheWrappedFactory pins the table
// entry itself. The factory tests above build the wrapper by hand, so
// registering core's bare factory instead would leave them all green
// while the initializer never runs in any assembled runtime.
func TestDelegationCapabilityRegistersTheWrappedFactory(t *testing.T) {
	registry := resource.NewRegistry()
	var found bool
	for _, capability := range appCapabilities(&Options{}) {
		if capability.Name() != "delegation" {
			continue
		}
		found = true
		registrar, ok := capability.(craft.Registrar)
		if !ok {
			t.Fatalf("delegation capability = %T, want a craft.Registrar",
				capability)
		}
		if err := registrar.Register(registry); err != nil {
			t.Fatalf("register the delegation capability: %v", err)
		}
	}
	if !found {
		t.Fatal("the capability table registers no delegation service")
	}
	factory, ok := registry.Lookup(sdkdelegation.ServiceKind, "local")
	if !ok {
		t.Fatal("the delegation capability registered no local service")
	}
	for _, dep := range factory.Spec().Deps {
		if dep.Name != delegationSessionsDep {
			continue
		}
		if !dep.Required {
			t.Fatal("the registered delegation factory does not require the session store")
		}
		return
	}
	t.Fatalf("the registered delegation factory carries no %q dependency: %+v",
		delegationSessionsDep, factory.Spec().Deps)
}
