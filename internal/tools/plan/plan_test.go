package plan

import (
	"context"
	"path/filepath"
	"testing"
)

func TestUpdatePlanReplacesSnapshot(t *testing.T) {
	store := NewStore("")
	tool, err := New(store)
	if err != nil {
		t.Fatalf("plan.New: %v", err)
	}
	tools := tool.Tools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	out, err := tools[0].Execute(context.Background(),
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
	latest, ok := store.Latest()
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
	if _, err := tools[0].Execute(context.Background(),
		`{"plan":[{"step":"Verify","status":"in_progress"}]}`); err != nil {
		t.Fatalf("update_plan replace: %v", err)
	}
	latest, _ = store.Latest()
	if latest.Explanation != "" || len(latest.Items) != 1 ||
		latest.Items[0].Step != "Verify" {
		t.Errorf("replaced plan = %+v", latest)
	}
}

func TestUpdatePlanValidation(t *testing.T) {
	store := NewStore("")
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
		{"two in progress", `{"plan":[
			{"step":"a","status":"in_progress"},
			{"step":"b","status":"in_progress"}]}`},
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
	latest, ok := store.Latest()
	if !ok || latest.Items[0].Step != "keep" {
		t.Errorf("plan was clobbered: %+v", latest)
	}
}

func TestPlanPersistsAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	store := NewStore(path)
	if _, err := store.Update(UpdatePlanArgs{
		Explanation: strptr("persist me"),
		Plan: []PlanItem{
			{Step: "Implement", Status: StatusInProgress},
			{Step: "Verify", Status: StatusPending},
		},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	// A fresh store over the same path must see the persisted plan.
	reopened := NewStore(path)
	got, ok := reopened.Latest()
	if !ok {
		t.Fatal("persisted plan not found")
	}
	if got.Explanation != "persist me" || len(got.Items) != 2 ||
		got.Items[0].Status != StatusInProgress {
		t.Errorf("reopened plan = %+v", got)
	}
}

func TestStoreWithoutPathStaysEmpty(t *testing.T) {
	store := NewStore("")
	if _, ok := store.Latest(); ok {
		t.Fatal("empty store must not report a plan")
	}
}

func strptr(s string) *string { return &s }
