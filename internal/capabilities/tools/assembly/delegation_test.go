package assembly

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/subagents"
)

// TestDelegationMiddlewareInstallsOnlyWhenNeeded pins the two shapes
// that mean "nothing to enforce": no policy resource at all, and a
// policy with neither list. Both leave the chain untouched.
func TestDelegationMiddlewareInstallsOnlyWhenNeeded(t *testing.T) {
	var missing *subagents.Policy
	if mw := delegationMiddleware(missing); mw != nil {
		t.Fatal("the nil policy installed a middleware")
	}
	if mw := delegationMiddleware(subagents.NewPolicy(nil, nil)); mw != nil {
		t.Fatal("an empty policy installed a middleware")
	}
	if mw := delegationMiddleware(subagents.NewPolicy([]string{"r"}, nil)); mw == nil {
		t.Fatal("a restricting policy did not install a middleware")
	}
}

// TestDelegationMiddlewareRefusesRestrictedTargets pins the enforcement
// point: the delegate tool call. Hiding a target from the listing is a
// hint for the model; a call that names a restricted target anyway must
// be refused here, before it reaches the delegation service.
func TestDelegationMiddlewareRefusesRestrictedTargets(t *testing.T) {
	ran := 0
	next := func(context.Context, message.ToolCall) message.ToolResult {
		ran++
		return message.ToolResult{
			CallID: "call", Content: message.NewTextContent("ran"),
		}
	}
	dispatch := delegationMiddleware(
		subagents.NewPolicy([]string{"researcher", "writer*"}, nil),
	)(next)

	call := func(t *testing.T, name string, args any) message.ToolCall {
		t.Helper()
		call, err := message.NewToolCall("call", name, args)
		if err != nil {
			t.Fatalf("build %s call: %v", name, err)
		}
		return call
	}

	// A different tool is not the middleware's business.
	if res := dispatch(context.Background(), call(t, "list_dir", map[string]any{})); res.IsError {
		t.Fatalf("list_dir was refused: %+v", res)
	}
	// An allowed target goes through.
	if res := dispatch(context.Background(), call(t, delegatedToolID, map[string]any{"target": "writer-two"})); res.IsError {
		t.Fatalf("an allowed target was refused: %+v", res)
	}
	if ran != 2 {
		t.Fatalf("the next dispatch ran %d times, want 2", ran)
	}

	// A restricted target is refused with a result, not an error the
	// caller could mistake for a transport failure.
	res := dispatch(context.Background(), call(t, delegatedToolID, map[string]any{"target": "reviewer"}))
	if !res.IsError {
		t.Fatalf("a restricted target was allowed: %+v", res)
	}
	if res.CallID != "call" {
		t.Fatalf("refusal lost the call id: %q", res.CallID)
	}
	text := res.Content.Text()
	if !strings.Contains(text, `"reviewer"`) {
		t.Fatalf("refusal does not name the target: %q", text)
	}
	if !strings.Contains(text, "allowed targets: researcher, writer*") {
		t.Fatalf("refusal does not report the policy: %q", text)
	}
	if ran != 2 {
		t.Fatalf("a refused call still reached the next dispatch (%d runs)", ran)
	}
}

// TestDelegationMiddlewareLeavesUnjudgeableCallsAlone pins the cases
// where the middleware has no verdict to give: the arguments are absent,
// unreadable, or carry no target. The tool owns those messages.
func TestDelegationMiddlewareLeavesUnjudgeableCallsAlone(t *testing.T) {
	ran := 0
	next := func(context.Context, message.ToolCall) message.ToolResult {
		ran++
		return message.ToolResult{
			CallID: "call", Content: message.NewTextContent("ran"),
		}
	}
	dispatch := delegationMiddleware(
		subagents.NewPolicy([]string{"researcher"}, nil),
	)(next)

	cases := map[string]message.ToolCall{
		"empty arguments": {
			ID: "call", Name: delegatedToolID, Arguments: nil,
		},
		"blank arguments": {
			ID: "call", Name: delegatedToolID, Arguments: json.RawMessage("  "),
		},
		"unreadable arguments": {
			ID: "call", Name: delegatedToolID, Arguments: json.RawMessage("{"),
		},
		"no target field": {
			ID: "call", Name: delegatedToolID, Arguments: json.RawMessage(`{"other":1}`),
		},
		"blank target": {
			ID: "call", Name: delegatedToolID, Arguments: json.RawMessage(`{"target":"  "}`),
		},
	}
	for name, call := range cases {
		if res := dispatch(context.Background(), call); res.IsError {
			t.Errorf("%s was refused: %+v", name, res)
		}
	}
	if ran != len(cases) {
		t.Fatalf("the next dispatch ran %d times, want %d", ran, len(cases))
	}
}

// TestDelegationTargetReadsTheName covers the small parser directly,
// including the whitespace trim the policy relies on.
func TestDelegationTargetReadsTheName(t *testing.T) {
	for args, want := range map[string]string{
		`{"target":" researcher "}`: "researcher",
		`{"target":"writer"}`:       "writer",
	} {
		target, ok := delegationTarget(args)
		if !ok || target != want {
			t.Errorf("delegationTarget(%s) = %q, %v, want %q, true",
				args, target, ok, want)
		}
	}
	for _, args := range []string{"", "  ", "{", `{"target":""}`, `{"t":"x"}`} {
		if target, ok := delegationTarget(args); ok {
			t.Errorf("delegationTarget(%s) = %q, true, want no verdict", args, target)
		}
	}
}
