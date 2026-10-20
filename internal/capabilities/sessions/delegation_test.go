package sessions

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/foundation/profile"
)

// newModeTestStore opens a migrated store in a temp dir.
func newModeTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.CloseDB(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

// delegatedRequest is the request shape the delegate tool hands the
// initializer, carrying one run attribute.
func delegatedRequest(attributes map[string]string) sdkdelegation.AsyncRequest {
	return sdkdelegation.AsyncRequest{
		Request: sdkdelegation.Request{Attributes: attributes},
	}
}

// storedMode reads the settings document without the workspace
// fallback, so "never written" is distinguishable from "written as
// workspace" (which is what the subagent would silently get anyway).
func storedMode(t *testing.T, store *Store, id string) string {
	t.Helper()
	mode, err := store.db.Mode(context.Background(), id)
	if err != nil {
		t.Fatalf("read stored mode %s: %v", id, err)
	}
	return mode
}

// TestModeInitializerWithoutStore pins the degenerate deployment: with
// no session store there is no row to write, and installing a no-op
// hook would only pretend the policy was applied.
func TestModeInitializerWithoutStore(t *testing.T) {
	if init := ModeInitializer(nil); init != nil {
		t.Fatal("a nil store installed a session initializer")
	}
}

// TestModeInitializerAppliesCallerMode is the fix itself: the mode the
// caller's delegate call carries lands on the subagent's own session
// before its turn starts, so the sandbox and the tool policies that read
// the store resolve the caller's mode instead of the confined default.
func TestModeInitializerAppliesCallerMode(t *testing.T) {
	store := newModeTestStore(t)
	init := ModeInitializer(store)
	ctx := context.Background()
	key := session.Key{AgentID: "writer", ContextID: "ctx-subagent"}

	if err := init(ctx, key, delegatedRequest(map[string]string{
		ModeAttribute: string(ModeYOLO),
	})); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if got := storedMode(t, store, key.ContextID); got != string(ModeYOLO) {
		t.Fatalf("subagent session mode = %q, want yolo", got)
	}
	mode, err := store.Mode(ctx, key.ContextID)
	if err != nil || mode != ModeYOLO {
		t.Fatalf("resolved mode = %q, %v; want yolo", mode, err)
	}
}

// TestModeInitializerLeavesUndeclaredDelegationsAlone pins that an
// absent attribute is not a mode: automation-driven and otherwise
// context-less delegations keep the store default, and no settings
// document is invented for them.
func TestModeInitializerLeavesUndeclaredDelegationsAlone(t *testing.T) {
	store := newModeTestStore(t)
	init := ModeInitializer(store)
	key := session.Key{AgentID: "writer", ContextID: "ctx-subagent"}

	for name, req := range map[string]sdkdelegation.AsyncRequest{
		"no attributes":       delegatedRequest(nil),
		"other keys":          delegatedRequest(map[string]string{"oc.tenant": "acme"}),
		"blank mode":          delegatedRequest(map[string]string{ModeAttribute: "   "}),
		"attributes nil, req": {},
	} {
		if err := init(context.Background(), key, req); err != nil {
			t.Errorf("%s: initialize: %v", name, err)
		}
		if got := storedMode(t, store, key.ContextID); got != "" {
			t.Errorf("%s: wrote mode %q, want no settings document", name, got)
		}
	}
	if mode, err := store.Mode(context.Background(), key.ContextID); err != nil || mode != freshSessionMode() {
		t.Errorf("resolved mode = %q, %v; want the store default %q",
			mode, err, freshSessionMode())
	}
}

// TestModeInitializerRefusesWhatItCannotApply pins the fail-closed
// contract: a policy that cannot be written must fail the delegation,
// not run the subagent under a wider default. The hook names the target
// and the mode, because the service wraps the error with the session id
// only.
func TestModeInitializerRefusesWhatItCannotApply(t *testing.T) {
	store := newModeTestStore(t)
	init := ModeInitializer(store)
	ctx := context.Background()

	if err := init(ctx, session.Key{AgentID: "writer", ContextID: "ctx-a"},
		delegatedRequest(map[string]string{ModeAttribute: "unconfined"})); err == nil {
		t.Fatal("an unknown mode was accepted")
	} else if !strings.Contains(err.Error(), "unconfined") {
		t.Fatalf("error %q does not name the mode it refused", err)
	}

	if err := init(ctx, session.Key{AgentID: "writer"},
		delegatedRequest(map[string]string{ModeAttribute: string(ModeYOLO)})); err == nil {
		t.Fatal("a delegation without a session id was accepted")
	} else if !strings.Contains(err.Error(), "writer") {
		t.Fatalf("error %q does not name the target it refused for", err)
	}

	if profile.YoloOnly() {
		if err := init(ctx, session.Key{AgentID: "writer", ContextID: "ctx-b"},
			delegatedRequest(map[string]string{ModeAttribute: string(ModeWorkspace)})); err == nil {
			t.Fatal("a confined mode was accepted in a yolo-only build")
		} else if !strings.Contains(err.Error(), "yolo") {
			t.Fatalf("error %q does not say why the mode was refused", err)
		}
	}
}

// TestModeInitializerSurvivesTheAsyncQueue pins the async half. An
// asynchronous delegation is queued before it runs, so the mode only
// reaches the initializer if it survives the request's JSON round trip
// through the backend — and an async subagent losing its policy would
// look exactly like a sync one that never got it.
func TestModeInitializerSurvivesTheAsyncQueue(t *testing.T) {
	store := newModeTestStore(t)
	queued, err := json.Marshal(sdkdelegation.AsyncRequest{
		Request: sdkdelegation.Request{
			Target:     "writer",
			Input:      "write the changelog",
			Attributes: map[string]string{ModeAttribute: string(ModeYOLO)},
		},
		Caller: "assistant",
		Depth:  1,
	})
	if err != nil {
		t.Fatalf("encode the queued request: %v", err)
	}
	var decoded sdkdelegation.AsyncRequest
	if err := json.Unmarshal(queued, &decoded); err != nil {
		t.Fatalf("decode the queued request: %v", err)
	}
	key := session.Key{AgentID: "writer", ContextID: "ctx-async"}
	if err := ModeInitializer(store)(context.Background(), key, decoded); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if got := storedMode(t, store, key.ContextID); got != string(ModeYOLO) {
		t.Fatalf("queued subagent session mode = %q, want yolo", got)
	}
}

// callerContext is the execution context a call inside a run of the
// given conversation sees.
func callerContext(conversationID string) context.Context {
	return agent.WithRunInfo(context.Background(), agent.RunInfo{
		Identity: agent.Identity{AgentID: "assistant", ConversationID: conversationID},
	})
}

// TestModeFromContextSeparatesUnknownFromUnresolvable pins two of the
// three answers the callers depend on: a resolvable mode, and nothing to
// resolve. The third — a store that could not answer — is a separate
// test, because it must not be folded into the second: that is how a
// caller's policy disappears the moment the database hiccups.
func TestModeFromContextSeparatesUnknownFromUnresolvable(t *testing.T) {
	ctx := context.Background()
	if mode, ok, err := ModeFromContext(ctx, nil); ok || err != nil || mode != ModeWorkspace {
		t.Fatalf("no store: mode = %q, ok = %v, err = %v", mode, ok, err)
	}

	store := newModeTestStore(t)
	if _, ok, err := ModeFromContext(ctx, store); ok || err != nil {
		t.Fatalf("no run info: ok = %v, err = %v", ok, err)
	}

	if err := store.SetMode(ctx, "conv-caller", ModeYOLO); err != nil {
		t.Fatal(err)
	}
	mode, ok, err := ModeFromContext(callerContext("conv-caller"), store)
	if err != nil || !ok || mode != ModeYOLO {
		t.Fatalf("resolved mode = %q, ok = %v, err = %v", mode, ok, err)
	}
}

// TestModeFromContextReportsAnUnreadableStore pins the third answer: a
// store that cannot say what the session's mode is comes back as an
// error, not as "this run has no policy". In the yoloonly build the mode
// never involves the database, so there is nothing to fail — the
// fail-closed direction is unreachable there.
func TestModeFromContextReportsAnUnreadableStore(t *testing.T) {
	if profile.YoloOnly() {
		t.Skip("the yoloonly build resolves every mode without the database")
	}
	store := newModeTestStore(t)
	// Closing twice is a no-op, so the cleanup registered by the helper
	// still succeeds.
	if err := store.CloseDB(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, ok, err := ModeFromContext(callerContext("conv-caller"), store); err == nil || ok {
		t.Fatalf("unreadable store: ok = %v, err = %v; want an error", ok, err)
	}
}
