package plan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"

	"github.com/GizClaw/opencraft/internal/sessions"
)

func newSessionsStore(t *testing.T) *sessions.Store {
	t.Helper()
	store, err := sessions.New(t.TempDir(), 40)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustSessions(t *testing.T, root string) *sessions.Store {
	t.Helper()
	store, err := sessions.New(root, 40)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// sessionCtx wraps ctx with the RunInfo flowcraft injects during graph
// execution, so update_plan can resolve its agent/session identity.
func sessionCtx(agentID, sessionID string) context.Context {
	return agent.WithRunInfo(context.Background(), agent.RunInfo{
		Identity: agent.Identity{
			AgentID:        agentID,
			ConversationID: sessionID,
		},
	})
}

func TestUpdatePlanReplacesSnapshot(t *testing.T) {
	store := NewStore(newSessionsStore(t))
	tool, err := New(store)
	if err != nil {
		t.Fatalf("plan.New: %v", err)
	}
	tools := tool.Tools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	out, err := tools[0].Execute(sessionCtx("assistant", "sess-1"),
		`{"explanation":"kick off","plan":[
			{"step":"Inspect behavior","status":"in_progress"},
			{"step":"Patch failing path","status":"pending"},
			{"step":"Run focused tests","status":"pending"}]}`)
	if err != nil {
		t.Fatalf("update_plan: %v", err)
	}
	if out != "Plan updated" {
		t.Errorf("result = %q, want %q", out, "Plan updated")
	}
	latest, ok := store.Latest("assistant", "sess-1")
	if !ok {
		t.Fatal("no plan stored")
	}
	if latest.Explanation != "kick off" || len(latest.Items) != 3 {
		t.Errorf("stored plan = %+v", latest)
	}
	if latest.Items[0].Status != StatusInProgress {
		t.Errorf("item status = %q", latest.Items[0].Status)
	}

	// A second call fully replaces the previous snapshot.
	if _, err := tools[0].Execute(sessionCtx("assistant", "sess-1"),
		`{"plan":[{"step":"Verify","status":"in_progress"}]}`); err != nil {
		t.Fatalf("update_plan replace: %v", err)
	}
	latest, _ = store.Latest("assistant", "sess-1")
	if latest.Explanation != "" || len(latest.Items) != 1 ||
		latest.Items[0].Step != "Verify" {
		t.Errorf("replaced plan = %+v", latest)
	}
}

func TestPlanIsolatedByAgentAndSession(t *testing.T) {
	store := NewStore(newSessionsStore(t))
	tool := MustNew(store).Tools()[0]

	for _, tc := range []struct {
		agentID, sessionID, step string
	}{
		{"assistant", "sess-1", "a1s1"},
		{"assistant", "sess-2", "a1s2"},
		{"reviewer", "sess-1", "a2s1"},
	} {
		if _, err := tool.Execute(sessionCtx(tc.agentID, tc.sessionID),
			`{"plan":[{"step":"`+tc.step+`","status":"in_progress"}]}`); err != nil {
			t.Fatalf("update %s/%s: %v", tc.agentID, tc.sessionID, err)
		}
	}

	for _, tc := range []struct {
		agentID, sessionID, want string
	}{
		{"assistant", "sess-1", "a1s1"},
		{"assistant", "sess-2", "a1s2"},
		{"reviewer", "sess-1", "a2s1"},
		// A different agent in the same session must not see the plan.
		{"reviewer", "sess-2", ""},
		// The same agent in a different session must not see it either.
		{"assistant", "sess-3", ""},
	} {
		got, ok := store.Latest(tc.agentID, tc.sessionID)
		if tc.want == "" {
			if ok {
				t.Errorf("%s/%s: unexpected plan %+v", tc.agentID, tc.sessionID, got)
			}
			continue
		}
		if !ok || got.Items[0].Step != tc.want {
			t.Errorf("%s/%s: plan = %+v, want step %q",
				tc.agentID, tc.sessionID, got, tc.want)
		}
	}
}

func TestKeyFromContext(t *testing.T) {
	agentID, sessionID := KeyFromContext(
		sessionCtx("assistant", "sess-1"))
	if agentID != "assistant" || sessionID != "sess-1" {
		t.Errorf("KeyFromContext = %q/%q", agentID, sessionID)
	}
	agentID, sessionID = KeyFromContext(context.Background())
	if agentID != "default" || sessionID != "default" {
		t.Errorf("KeyFromContext background = %q/%q", agentID, sessionID)
	}
}

func TestUpdatePlanValidation(t *testing.T) {
	store := NewStore(newSessionsStore(t))
	tool := MustNew(store).Tools()[0]
	ctx := context.Background()

	cases := []struct {
		name string
		args string
	}{
		{"missing plan", `{}`},
		{"empty plan", `{"plan":[]}`},
		{"missing step", `{"plan":[{"status":"pending"}]}`},
		{"empty step", `{"plan":[{"step":"  ","status":"pending"}]}`},
		{"missing status", `{"plan":[{"step":"x"}]}`},
		{"bad status", `{"plan":[{"step":"x","status":"done"}]}`},
		{"unknown top field", `{"plan":[{"step":"x","status":"pending"}],"nope":1}`},
		{"unknown item field", `{"plan":[{"step":"x","status":"pending","nope":1}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tool.Execute(ctx, tc.args); err == nil {
				t.Errorf("args %s: expected error", tc.args)
			}
		})
	}

	// Invalid input must not clobber an existing plan.
	if _, err := tool.Execute(ctx,
		`{"plan":[{"step":"keep","status":"pending"}]}`); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if _, err := tool.Execute(ctx,
		`{"plan":[{"step":"bad","status":"nope"}]}`); err == nil {
		t.Fatal("expected validation error")
	}
	latest, ok := store.Latest("default", "default")
	if !ok || latest.Items[0].Step != "keep" {
		t.Errorf("plan was clobbered: %+v", latest)
	}
}

// TestPlanAllowsMultipleInProgress mirrors codex-rs: parallel work can
// keep several steps in_progress at once; the tool must accept and
// persist the snapshot.
func TestPlanAllowsMultipleInProgress(t *testing.T) {
	store := NewStore(newSessionsStore(t))
	tool := MustNew(store).Tools()[0]
	ctx := sessionCtx("assistant", "sess-1")

	args := `{"explanation":"three parallel reviews","plan":[
		{"step":"review agents","status":"in_progress"},
		{"step":"review app","status":"in_progress"},
		{"step":"review config","status":"in_progress"}]}`
	if _, err := tool.Execute(ctx, args); err != nil {
		t.Fatalf("update with three in_progress steps: %v", err)
	}
	latest, ok := store.Latest("assistant", "sess-1")
	if !ok {
		t.Fatal("plan not persisted")
	}
	if latest.Explanation != "three parallel reviews" {
		t.Fatalf("explanation = %q", latest.Explanation)
	}
	if len(latest.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(latest.Items))
	}
	for i, item := range latest.Items {
		if item.Status != StatusInProgress {
			t.Fatalf("item %d status = %q, want in_progress", i, item.Status)
		}
	}
}

func TestPlanPersistsAcrossStores(t *testing.T) {
	root := t.TempDir()
	store := NewStore(mustSessions(t, root))
	if _, err := store.Update("assistant", "sess-1", UpdatePlanArgs{
		Explanation: strptr("persist me"),
		Plan: []PlanItem{
			{Step: "Implement", Status: StatusInProgress},
			{Step: "Verify", Status: StatusPending},
		},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	// The plan lands under <root>/<session>/plans.json, keyed by agent.
	want := filepath.Join(root, "sess-1", "plans.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("plan file %s: %v", want, err)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	var byAgent map[string]Plan
	if err := json.Unmarshal(data, &byAgent); err != nil {
		t.Fatalf("plans.json: %v", err)
	}
	if _, ok := byAgent["assistant"]; !ok {
		t.Fatalf("plans.json missing agent key: %s", data)
	}

	// A fresh plan store over the same session root must see the plan.
	reopened := NewStore(mustSessions(t, root))
	got, ok := reopened.Latest("assistant", "sess-1")
	if !ok {
		t.Fatal("persisted plan not found")
	}
	if got.Explanation != "persist me" || len(got.Items) != 2 ||
		got.Items[0].Status != StatusInProgress {
		t.Errorf("reopened plan = %+v", got)
	}
}

func TestPlansFileKeyedByAgent(t *testing.T) {
	root := t.TempDir()
	store := NewStore(mustSessions(t, root))
	if _, err := store.Update("assistant", "sess-1", UpdatePlanArgs{
		Plan: []PlanItem{{Step: "agent a", Status: StatusInProgress}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update("reviewer", "sess-1", UpdatePlanArgs{
		Plan: []PlanItem{{Step: "agent b", Status: StatusPending}},
	}); err != nil {
		t.Fatal(err)
	}

	// One file per session, both agent entries present.
	data, err := os.ReadFile(filepath.Join(root, "sess-1", "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	var byAgent map[string]Plan
	if err := json.Unmarshal(data, &byAgent); err != nil {
		t.Fatal(err)
	}
	if len(byAgent) != 2 {
		t.Fatalf("plans.json agent entries = %d, want 2: %s", len(byAgent), data)
	}

	// A fresh store sees both entries, still isolated per agent.
	reopened := NewStore(mustSessions(t, root))
	gotA, okA := reopened.Latest("assistant", "sess-1")
	gotB, okB := reopened.Latest("reviewer", "sess-1")
	if !okA || gotA.Items[0].Step != "agent a" ||
		!okB || gotB.Items[0].Step != "agent b" {
		t.Errorf("reopened plans = %+v / %+v", gotA, gotB)
	}
}

func TestUpdateDoesNotClobberOtherAgent(t *testing.T) {
	root := t.TempDir()
	first := NewStore(mustSessions(t, root))
	if _, err := first.Update("assistant", "sess-1", UpdatePlanArgs{
		Plan: []PlanItem{{Step: "keep me", Status: StatusInProgress}},
	}); err != nil {
		t.Fatal(err)
	}
	// A second store over the same root updates another agent in the
	// same session: the first agent's plan must survive the rewrite.
	second := NewStore(mustSessions(t, root))
	if _, err := second.Update("reviewer", "sess-1", UpdatePlanArgs{
		Plan: []PlanItem{{Step: "new agent", Status: StatusPending}},
	}); err != nil {
		t.Fatal(err)
	}
	reopened := NewStore(mustSessions(t, root))
	got, ok := reopened.Latest("assistant", "sess-1")
	if !ok || got.Items[0].Step != "keep me" {
		t.Errorf("assistant plan clobbered: %+v", got)
	}
}

func TestEmptySessionHasNoPlan(t *testing.T) {
	store := NewStore(newSessionsStore(t))
	if _, ok := store.Latest("assistant", "sess-1"); ok {
		t.Fatal("session without a plan file must not report one")
	}
}

func TestPlanDone(t *testing.T) {
	cases := []struct {
		name  string
		items []PlanItem
		done  bool
	}{
		{"all completed", []PlanItem{
			{Step: "a", Status: StatusCompleted},
			{Step: "b", Status: StatusCompleted},
		}, true},
		{"mixed", []PlanItem{
			{Step: "a", Status: StatusCompleted},
			{Step: "b", Status: StatusPending},
		}, false},
		{"in progress", []PlanItem{
			{Step: "a", Status: StatusInProgress},
		}, false},
		{"empty", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Plan{Items: tc.items}).Done(); got != tc.done {
				t.Errorf("Done() = %v, want %v", got, tc.done)
			}
		})
	}
}

func strptr(s string) *string { return &s }
