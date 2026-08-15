package plan

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanCreateAndUpdate(t *testing.T) {
	store := NewStore("")
	tool, err := New(store)
	if err != nil {
		t.Fatalf("plan.New: %v", err)
	}
	tools := tool.Tools()
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}

	out, err := tools[0].Execute(context.Background(),
		`{"plan":"1. inspect\n2. implement\n3. verify"}`)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	var created Plan
	if err := decode(out, &created); err != nil {
		t.Fatalf("decode plan result: %v (%s)", err, out)
	}
	if created.ID == "" || created.Status != "active" {
		t.Errorf("plan result: %+v", created)
	}

	got, err := tools[1].Execute(context.Background(),
		`{"plan_id":"`+created.ID+`","status":"completed","focus":"keep tests green"}`)
	if err != nil {
		t.Fatalf("update_plan: %v", err)
	}
	var updated Plan
	if err := decode(got, &updated); err != nil {
		t.Fatalf("decode update result: %v", err)
	}
	if updated.Status != "completed" || !strings.Contains(updated.Text, "keep tests green") {
		t.Errorf("updated plan: %+v", updated)
	}
}

func TestPlanValidation(t *testing.T) {
	store := NewStore("")
	tool := MustNew(store).Tools()
	if _, err := tool[0].Execute(context.Background(), `{"plan":""}`); err == nil {
		t.Error("plan with empty text should error")
	}
	if _, err := tool[1].Execute(context.Background(), `{}`); err == nil {
		t.Error("update_plan with no fields should error")
	}
	if _, err := tool[1].Execute(context.Background(), `{"plan_id":"nope","plan":"x"}`); err == nil {
		t.Error("update_plan unknown id should error")
	}
	// update without plan_id targets the latest plan.
	if _, err := tool[0].Execute(context.Background(), `{"plan":"first"}`); err != nil {
		t.Fatalf("plan: %v", err)
	}
	out, err := tool[1].Execute(context.Background(), `{"status":"cancelled"}`)
	if err != nil {
		t.Fatalf("update_plan latest: %v", err)
	}
	if !strings.Contains(out, `"status":"cancelled"`) {
		t.Errorf("update_plan latest result: %s", out)
	}
}

func TestPlanPersistsAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	store := NewStore(path)
	p, err := store.Create("1. inspect\n2. implement", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Update(p.ID, "", "completed", "keep tests green"); err != nil {
		t.Fatalf("update: %v", err)
	}

	// A fresh store over the same path must see the persisted plan.
	reopened := NewStore(path)
	got, ok := reopened.Get(p.ID)
	if !ok {
		t.Fatalf("persisted plan %q not found", p.ID)
	}
	if got.Status != "completed" || !strings.Contains(got.Text, "keep tests green") {
		t.Errorf("reopened plan = %+v", got)
	}
	latest, ok := reopened.Latest()
	if !ok || latest.ID != p.ID {
		t.Errorf("latest = %+v, want %q", latest, p.ID)
	}
}

func TestPlanReadTools(t *testing.T) {
	store := NewStore("")
	tools := MustNew(store).Tools()
	ctx := context.Background()

	out, err := tools[0].Execute(ctx, `{"plan":"first plan"}`)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	var created Plan
	if err := decode(out, &created); err != nil {
		t.Fatalf("decode plan result: %v (%s)", err, out)
	}

	// get_plan by id.
	out, err = tools[2].Execute(ctx,
		`{"plan_id":"`+created.ID+`"}`)
	if err != nil {
		t.Fatalf("get_plan: %v", err)
	}
	if !strings.Contains(out, "first plan") || !strings.Contains(out, created.ID) {
		t.Errorf("get_plan result: %s", out)
	}

	// get_plan without id reads the latest plan.
	out, err = tools[2].Execute(ctx, `{}`)
	if err != nil {
		t.Fatalf("get_plan latest: %v", err)
	}
	if !strings.Contains(out, created.ID) {
		t.Errorf("get_plan latest result: %s", out)
	}

	// get_plan unknown id errors.
	if _, err := tools[2].Execute(ctx, `{"plan_id":"nope"}`); err == nil {
		t.Error("get_plan unknown id should error")
	}

	// list_plans returns the plan list.
	out, err = tools[3].Execute(ctx, `{}`)
	if err != nil {
		t.Fatalf("list_plans: %v", err)
	}
	if !strings.Contains(out, `"plans"`) || !strings.Contains(out, created.ID) {
		t.Errorf("list_plans result: %s", out)
	}

	// Empty store lists an empty array instead of null.
	empty := NewStore("")
	emptyOut, err := MustNew(empty).Tools()[3].Execute(ctx, `{}`)
	if err != nil {
		t.Fatalf("list_plans empty: %v", err)
	}
	if !strings.Contains(emptyOut, `"plans":[]`) {
		t.Errorf("list_plans empty result: %s", emptyOut)
	}
}

func decode(data string, v any) error {
	return json.Unmarshal([]byte(data), v)
}
