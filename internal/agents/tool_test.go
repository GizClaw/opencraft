package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

func testTool(t *testing.T) *Tool {
	t.Helper()
	reg := newFakeRegistrar()
	lc, _ := newTestLifecycle(t, reg)
	return MustNew(lc)
}

func TestCreateToolDefinitionAndExecute(t *testing.T) {
	tool := testTool(t)
	desc := tool.Tools()[0].Definition()
	if desc.Name != CreateName {
		t.Errorf("tool name = %s, want %s", desc.Name, CreateName)
	}
	if !strings.Contains(desc.Description, "delegation target") {
		t.Errorf("description should mention delegation: %s", desc.Description)
	}

	got, err := tool.Tools()[0].Execute(context.Background(),
		`{"name":"researcher","description":"Summarizes code","instructions":"Explore and summarize.","tools":"read_only"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatal(err)
	}
	if result["name"] != "researcher" || result["status"] != "registered" {
		t.Errorf("result = %v", result)
	}
	if hint, _ := result["hint"].(string); !strings.Contains(hint, "delegate") {
		t.Errorf("hint should mention delegate: %s", hint)
	}
}

func TestCreateToolRejectsUnknownField(t *testing.T) {
	tool := testTool(t)
	if _, err := tool.Tools()[0].Execute(context.Background(),
		`{"name":"x","description":"d","instructions":"i","bogus":1}`); err == nil {
		t.Fatal("unknown field accepted")
	} else if !errdefs.IsValidation(err) {
		t.Errorf("error = %v, want validation", err)
	}
}

func TestRemoveToolExecute(t *testing.T) {
	tool := testTool(t)
	lc := tool.lifecycle
	if _, err := lc.Create(context.Background(), AgentSpec{
		Name:         "worker",
		Description:  "desc",
		Instructions: "do it",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := tool.Tools()[1].Execute(context.Background(),
		`{"name":"worker"}`)
	if err != nil {
		t.Fatalf("Execute remove: %v", err)
	}
	if !strings.Contains(got, `"status":"unregistered"`) {
		t.Errorf("remove result = %s", got)
	}
	if len(lc.List()) != 0 {
		t.Errorf("List after remove = %+v, want empty", lc.List())
	}
}
