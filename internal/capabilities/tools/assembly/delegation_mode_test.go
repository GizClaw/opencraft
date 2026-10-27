package assembly

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	tooldelegation "github.com/GizClaw/flowcraft/core/delegation/tool"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

// callerContext is the execution context a tool call sees inside a run
// of the given conversation.
func callerContext(conversationID string) context.Context {
	return agent.WithRunInfo(context.Background(), agent.RunInfo{
		Identity: agent.Identity{AgentID: "assistant", ConversationID: conversationID},
	})
}

// newModeStore opens the workspace session store the middleware reads.
func newModeStore(t *testing.T) *ocsessions.Store {
	t.Helper()
	store, err := sessionstore.Open(t, filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// TestDelegationModeMiddlewareStampsCallerMode pins the stamping side of
// the fix: the delegate call carries the caller's stored mode as a run
// attribute, which the delegate tool copies onto its request and the
// delegation service applies to the subagent session. The value comes
// from the caller's session row, never from the model's arguments.
func TestDelegationModeMiddlewareStampsCallerMode(t *testing.T) {
	store := newModeStore(t)
	if err := store.SetMode(context.Background(), "conv-caller", ocsessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}

	var stamped map[string]string
	var found bool
	next := func(ctx context.Context, _ message.ToolCall) message.ToolResult {
		stamped, found = sdkdelegation.RunAttributesFromContext(ctx)
		return message.ToolResult{CallID: "call", Content: message.NewTextContent("ran")}
	}
	dispatch := delegationModeMiddleware(store)(next)

	res := dispatch(callerContext("conv-caller"), message.ToolCall{
		ID: "call", Name: delegatedToolID,
	})
	if res.IsError {
		t.Fatalf("the delegate call was refused: %+v", res)
	}
	if !found {
		t.Fatal("the delegate call carried no run attributes")
	}
	if got := stamped[ocsessions.ModeAttribute]; got != string(ocsessions.ModeYOLO) {
		t.Fatalf("stamped mode = %q, want yolo", got)
	}
}

// TestDelegationModeMiddlewareLeavesOtherCallsAlone covers the calls
// that are not the middleware's business: another tool, and a delegation
// whose caller has no session to read a mode from.
func TestDelegationModeMiddlewareLeavesOtherCallsAlone(t *testing.T) {
	store := newModeStore(t)
	dispatch := delegationModeMiddleware(store)(func(
		ctx context.Context, _ message.ToolCall,
	) message.ToolResult {
		if _, found := sdkdelegation.RunAttributesFromContext(ctx); found {
			t.Error("a call outside the delegate tool was stamped")
		}
		return message.ToolResult{CallID: "call"}
	})

	dispatch(callerContext("conv-caller"), message.ToolCall{
		ID: "call", Name: "list_dir",
	})
	// No run info at all: there is no caller session, so there is no
	// mode to hand down. Stamping the store default here would invent a
	// policy for a run that never had one.
	dispatch(context.Background(), message.ToolCall{
		ID: "call", Name: delegatedToolID,
	})
}

// TestDelegationModeMiddlewareInstallsOnlyWithAStore pins that a
// deployment without the store stays exactly as it was: no middleware,
// no attributes, and the subagent keeps the store default.
func TestDelegationModeMiddlewareInstallsOnlyWithAStore(t *testing.T) {
	var missing *ocsessions.Store
	if mw := delegationModeMiddleware(missing); mw != nil {
		t.Fatal("a nil store installed a middleware")
	}
}

// TestDelegationModeMiddlewareKeepsAttributesStampedFurtherOut pins
// the layering: the mode is added to the caller-declared attributes a
// deployment may have stamped, not written over them. Run attributes are
// the channel for the whole execution policy, so an outer producer's
// keys that vanished here would break it without a word.
func TestDelegationModeMiddlewareKeepsAttributesStampedFurtherOut(t *testing.T) {
	store := newModeStore(t)
	if err := store.SetMode(context.Background(), "conv-caller", ocsessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}

	var stamped map[string]string
	next := func(ctx context.Context, _ message.ToolCall) message.ToolResult {
		stamped, _ = sdkdelegation.RunAttributesFromContext(ctx)
		return message.ToolResult{CallID: "call"}
	}
	dispatch := delegationModeMiddleware(store)(next)
	callCtx := sdkdelegation.WithRunAttributes(callerContext("conv-caller"),
		map[string]string{"oc.tenant": "acme", "oc.policy": "reviewed"})

	res := dispatch(callCtx, message.ToolCall{ID: "call", Name: delegatedToolID})
	if res.IsError {
		t.Fatalf("the delegate call was refused: %+v", res)
	}
	if got := stamped[ocsessions.ModeAttribute]; got != string(ocsessions.ModeYOLO) {
		t.Fatalf("stamped mode = %q, want yolo", got)
	}
	for key, want := range map[string]string{"oc.tenant": "acme", "oc.policy": "reviewed"} {
		if got := stamped[key]; got != want {
			t.Fatalf("attribute %q = %q, want %q (all: %+v)",
				key, got, want, stamped)
		}
	}
}

// TestDelegationModeMiddlewareRefusesAnUnreadableCallerMode pins the
// failure direction: when the store cannot say what mode the caller is
// in, the delegation fails instead of handing the subagent the confined
// default. That is the same silent downgrade this middleware exists to
// prevent, only with the database as the cause.
func TestDelegationModeMiddlewareRefusesAnUnreadableCallerMode(t *testing.T) {
	if profile.YoloOnly() {
		t.Skip("the yoloonly build resolves every mode without the database")
	}
	store := newModeStore(t)
	if err := store.CloseDB(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	var ran bool
	dispatch := delegationModeMiddleware(store)(func(
		context.Context, message.ToolCall,
	) message.ToolResult {
		ran = true
		return message.ToolResult{CallID: "call"}
	})
	res := dispatch(callerContext("conv-caller"),
		message.ToolCall{ID: "call", Name: delegatedToolID})
	if !res.IsError {
		t.Fatal("a delegate call ran with an unreadable caller mode")
	}
	if ran {
		t.Fatal("the delegate tool was reached anyway")
	}
	if text := res.Content.Text(); !strings.Contains(text, "permission mode") {
		t.Fatalf("refusal %q does not say what could not be read", text)
	}
}

// recordingDelegationService is the delegation service the real delegate
// tool hands its request to. It records the request, which is where a
// stamped run attribute has to end up.
type recordingDelegationService struct {
	requests []sdkdelegation.Request
}

func (s *recordingDelegationService) Delegate(
	_ context.Context, req sdkdelegation.Request,
) (sdkdelegation.Response, error) {
	s.requests = append(s.requests, req)
	return sdkdelegation.Response{
		ID: "delegation-1", Status: sdkdelegation.StatusSucceeded,
	}, nil
}

func (s *recordingDelegationService) Get(
	context.Context, string,
) (sdkdelegation.Response, error) {
	return sdkdelegation.Response{}, sdkdelegation.ErrRequestNotFound
}

// TestAssemblyStampsTheCallerModeOnTheRealDelegateCall drives the whole
// chain the runtime builds, with core's delegate tool rather than a
// stand-in: the factory resolves the session store from its
// dependencies, the middleware stamps the caller's mode on the call, and
// the tool copies it onto the request it sends. A middleware that is
// built but never installed, or an attribute stamped on a context the
// tool no longer reads, fails here.
func TestAssemblyStampsTheCallerModeOnTheRealDelegateCall(t *testing.T) {
	ctx := context.Background()
	store := newModeStore(t)
	if err := store.SetMode(ctx, "conv-caller", ocsessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}

	source, err := tooldelegation.NewSourceFactory().New(ctx, resource.NewTestInput(map[string]any{
		sdkdelegation.DirectoryDep: sdkdelegation.NewDirectory(),
	}))
	if err != nil {
		t.Fatalf("build the delegate source: %v", err)
	}
	value, err := (AssemblyFactory{}).New(ctx, resource.NewInput([]byte(`{}`), map[string]any{
		"tool":     source,
		"sessions": store,
	}, nil, nil))
	if err != nil {
		t.Fatalf("build assembly: %v", err)
	}
	assembly := value
	defer func() { _ = assembly.Close() }()

	service := &recordingDelegationService{}
	callCtx := agent.ContextWithHost(
		callerContext("conv-caller"),
		sdkdelegation.WithService(agent.NoopHost{}, service),
	)
	arguments := json.RawMessage(
		`{"mode":"sync","target":"writer","input":"write the changelog"}`)
	res := assembly.Execute(callCtx, message.ToolCall{
		ID: "call", Name: delegatedToolID, Arguments: arguments,
	})
	if res.IsError {
		t.Fatalf("the delegate call failed: %+v", res)
	}
	if len(service.requests) != 1 {
		t.Fatalf("the delegate tool sent %d requests, want 1",
			len(service.requests))
	}
	if got := service.requests[0].Attributes[ocsessions.ModeAttribute]; got != string(ocsessions.ModeYOLO) {
		t.Fatalf("delegation request mode = %q, want yolo", got)
	}
}
