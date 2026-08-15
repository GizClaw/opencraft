package plan

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanCreateAndUpdate(t *testing.T) {
	store := NewStore()
	tool, err := New(store)
	if err != nil {
		t.Fatalf("plan.New: %v", err)
	}
	tools := tool.Tools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
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
	store := NewStore()
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

func decode(data string, v any) error {
	return json.Unmarshal([]byte(data), v)
}
