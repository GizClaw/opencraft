package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"
)

// fakeDelegationService is a minimal sdkdelegation.Service used to probe
// host capability exposure; it is never actually invoked.
type fakeDelegationService struct{}

func (*fakeDelegationService) Delegate(
	context.Context, sdkdelegation.Request,
) (sdkdelegation.Response, error) {
	return sdkdelegation.Response{}, nil
}

func (*fakeDelegationService) Get(
	context.Context, string,
) (sdkdelegation.Response, error) {
	return sdkdelegation.Response{}, nil
}

// TestEphemeralTurnHostExposesDelegationService is a regression test
// for the opencraft delegation failure "host has no delegation
// service": the desktop starts every turn with session.WithEphemeral(),
// and the delegate tool resolves the delegation service through
// agent.CapabilityFromHost on the turn host. If the core session's
// ephemeral host wrapper hides inner capabilities (ephemeralHost
// without UnwrapHost), the lookup stops at the wrapper and delegation
// breaks. This test drives a real ephemeral turn through the runtime
// session manager and asserts the engine still sees the service.
func TestEphemeralTurnHostExposesDelegationService(t *testing.T) {
	ctx := context.Background()
	service := &fakeDelegationService{}
	seen := make(chan error, 1)

	engine := agent.EngineFunc(func(
		_ context.Context,
		_ agent.Run,
		host agent.Host,
		board *agent.Board,
	) (*agent.Board, error) {
		if _, ok := sdkdelegation.ServiceFromHost(host); !ok {
			seen <- fmt.Errorf(
				"delegation service not reachable through the ephemeral turn host")
			return board, nil
		}
		seen <- nil
		return board, nil
	})

	resolver := agentResolverFunc(func(id string) (*agent.Agent, bool) {
		if id != "assistant" {
			return nil, false
		}
		return &agent.Agent{ID: "assistant", Engine: engine}, true
	})

	// Mirror opencraft's host composition: the base host is wrapped with
	// the delegation service (delegationhostwrap) and the usage-observer
	// HostFuncs decorator; the session then wraps it in the core's
	// ephemeral host wrapper.
	hostFactory := coresession.HostFactoryFunc(func(
		_ context.Context,
		_ coresession.HostRequest,
	) (agent.Host, error) {
		base := agent.HostFuncs{Inner: agent.NoopHost{}}
		return sdkdelegation.WithService(base, service), nil
	})

	router := event.NewRouter(event.NewMemoryBus())
	t.Cleanup(func() { _ = router.Close() })
	manager, err := coresession.NewManager(resolver, hostFactory, router)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	key := coresession.Key{AgentID: "assistant", ContextID: "ctx-ephemeral"}
	lease, err := manager.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })

	turn, err := lease.Session().StartWithOptions(ctx, agent.Request{
		ContextID: key.ContextID,
		Message:   message.NewTextMessage(message.RoleUser, "hello"),
	}, coresession.WithEphemeral())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-seen; err != nil {
		t.Fatal(err)
	}
}

type agentResolverFunc func(string) (*agent.Agent, bool)

func (f agentResolverFunc) Instance(id string) (*agent.Agent, bool) {
	return f(id)
}
