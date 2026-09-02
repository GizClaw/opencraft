package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/runtime"
)

func confirmCtx(t *testing.T, choice string, cancelled bool) context.Context {
	t.Helper()
	meta := map[string]string{}
	if cancelled {
		meta[runtime.MetaStatus] = string(runtime.ReplyCancelled)
	} else {
		meta[runtime.MetaChoice] = choice
	}
	return agent.ContextWithHost(context.Background(), agent.HostFuncs{
		AskUserFn: func(
			context.Context, agent.UserPrompt,
		) (agent.UserReply, error) {
			return agent.UserReply{Metadata: meta}, nil
		},
	})
}

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

	got, err := tool.Tools()[0].Execute(confirmCtx(t, "yes", false),
		`{"name":"researcher","description":"Summarizes code","graph":"{\"name\":\"g\",\"entry\":\"llm\",\"nodes\":[{\"id\":\"llm\",\"type\":\"inference\",\"config\":{\"system_prompt\":\"SP\"}}],\"edges\":[{\"from\":\"llm\",\"to\":\"__end__\"}]}"}`)
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
		`{"name":"x","description":"d","graph":"{}","bogus":1}`); err == nil {
		t.Fatal("unknown field accepted")
	} else if !errdefs.IsValidation(err) {
		t.Errorf("error = %v, want validation", err)
	}
}

func TestRemoveToolExecute(t *testing.T) {
	tool := testTool(t)
	lc := tool.lifecycle
	if _, err := lc.Create(context.Background(), AgentSpec{
		Name:        "worker",
		Description: "desc",
		Graph:       testGraph,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := tool.Tools()[2].Execute(confirmCtx(t, "yes", false),
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

func TestUpdateToolDefinitionAndExecute(t *testing.T) {
	tool := testTool(t)
	lc := tool.lifecycle
	if _, err := lc.Create(context.Background(), AgentSpec{
		Name:        "worker",
		Description: "old description",
		Graph:       testGraph,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	def := tool.Tools()[1].Definition()
	if def.Name != UpdateName {
		t.Errorf("tool name = %s, want %s", def.Name, UpdateName)
	}
	if !strings.Contains(def.Description, "flowcraft-config") {
		t.Errorf("description should mention flowcraft-config: %s", def.Description)
	}

	got, err := tool.Tools()[1].Execute(confirmCtx(t, "yes", false),
		`{"name":"worker","description":"new description","graph":"{\"name\":\"g2\",\"entry\":\"llm\",\"nodes\":[{\"id\":\"llm\",\"type\":\"inference\",\"config\":{\"system_prompt\":\"SP2\"}}],\"edges\":[{\"from\":\"llm\",\"to\":\"__end__\"}]}"}`)
	if err != nil {
		t.Fatalf("Execute update: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatal(err)
	}
	if result["name"] != "worker" || result["status"] != "updated" {
		t.Errorf("result = %v", result)
	}
	if result["description"] != "new description" {
		t.Errorf("result description = %v", result["description"])
	}
	list := lc.List()
	if len(list) != 1 || list[0].Description != "new description" {
		t.Errorf("List after update = %+v", list)
	}
}

func TestUpdateToolRequiresExistingAgent(t *testing.T) {
	tool := testTool(t)
	if _, err := tool.Tools()[1].Execute(confirmCtx(t, "yes", false),
		`{"name":"ghost","description":"desc"}`); err == nil {
		t.Fatal("update of missing agent succeeded")
	} else if !errdefs.IsNotFound(err) {
		t.Errorf("error = %v, want NotFound", err)
	}
}

func TestCreateToolRequiresConfirmation(t *testing.T) {
	tool := testTool(t)
	out, err := tool.Tools()[0].Execute(confirmCtx(t, "", true),
		`{"name":"sneaky","description":"d","graph":"{}"}`)
	if err != nil {
		t.Fatalf("Execute(cancelled): %v", err)
	}
	if !strings.Contains(out, `"cancelled":true`) {
		t.Fatalf("cancelled output = %q", out)
	}
	if len(tool.lifecycle.List()) != 0 {
		t.Fatal("cancelled create must not register an agent")
	}
}

func TestUpdateToolRejectsUnknownField(t *testing.T) {
	tool := testTool(t)
	if _, err := tool.Tools()[1].Execute(context.Background(),
		`{"name":"worker","description":"desc","bogus":1}`); err == nil {
		t.Fatal("unknown field accepted")
	} else if !errdefs.IsValidation(err) {
		t.Errorf("error = %v, want validation", err)
	}
}
