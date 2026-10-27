package compact

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/message"
)

// textResponse is one successful condensation answer.
func textResponse(text string) inference.GenerateResponse {
	return inference.GenerateResponse{
		Message:      message.NewTextMessage(message.RoleAssistant, text),
		FinishReason: inference.FinishCompleted,
	}
}

// reasoningOnlyResponse is what a reasoning model returns when its output
// budget goes entirely into thinking: one reasoning part, no text, and the
// terminal finish reason max_output.
func reasoningOnlyResponse(reasoningTokens int64) inference.GenerateResponse {
	return inference.GenerateResponse{
		Message: message.Message{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ReasoningPart{Text: "thinking"},
			}},
		},
		FinishReason: inference.FinishMaxOutput,
		Usage: inference.Usage{
			Output: inference.OutputTokenUsage{ReasoningTokens: &reasoningTokens},
		},
	}
}

// condenseArgs wires the tool's transcript reader and returns the range that
// fold covers: the two messages these cases condense.
func condenseArgs(t *testing.T, tool *Tool, conversationID string, budget int) string {
	t.Helper()
	rows := rowsOf(
		textMessage(message.RoleUser, "m1"),
		textMessage(message.RoleAssistant, "m2"),
	)
	tool.rows = rows
	return foldArgs(conversationID, rows, budget)
}

// TestCondenseDisablesReasoningWhenTheDeploymentAllowsIt pins the preferred
// shape: a model that publishes a reasoning toggle gets reasoning switched
// off, so the whole output cap is available for the summary.
func TestCondenseDisablesReasoningWhenTheDeploymentAllowsIt(t *testing.T) {
	var intent *inference.TextIntent
	tool := &Tool{
		probe: func(context.Context, inference.GenerateRequest) condenseProbe {
			return condenseProbe{reasoningCanBeDisabled: true}
		},
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			intent = req.Input.Content.Intent.Text
			return textResponse("S1"), nil
		},
	}
	out, err := tool.Execute(context.Background(), condenseArgs(t, tool, "s-1", 4096))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := patchSummary(t, out.Text()); got != "S1" {
		t.Fatalf("summary = %q, want S1", got)
	}
	if intent == nil {
		t.Fatal("condense request carried no text intent")
	}
	if intent.ReasoningEnabled == nil || *intent.ReasoningEnabled {
		t.Fatalf("reasoning_enabled = %v, want false", intent.ReasoningEnabled)
	}
	if intent.ReasoningEffort != "" {
		t.Fatalf("reasoning effort = %q, want none beside the off switch",
			intent.ReasoningEffort)
	}
	if intent.MaxOutputTokens == nil || *intent.MaxOutputTokens != 4096 {
		t.Fatalf("max output = %v, want the summary budget (4096)",
			intent.MaxOutputTokens)
	}
}

// TestCondenseUsesTheSmallestEffortWhenReasoningCannotBeSwitchedOff covers
// the deployment shape the toggle probe cannot satisfy: reasoning stays on,
// so the cap has to carry a reserve for it and the request asks for the
// smallest effort the model publishes.
func TestCondenseUsesTheSmallestEffortWhenReasoningCannotBeSwitchedOff(t *testing.T) {
	var intent *inference.TextIntent
	tool := &Tool{
		probe: func(context.Context, inference.GenerateRequest) condenseProbe {
			return condenseProbe{effortNative: true}
		},
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			intent = req.Input.Content.Intent.Text
			return textResponse("S1"), nil
		},
	}
	if _, err := tool.Execute(
		context.Background(), condenseArgs(t, tool, "s-1", 4096),
	); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if intent == nil {
		t.Fatal("condense request carried no text intent")
	}
	if intent.ReasoningEffort != model.ReasoningMinimal {
		t.Fatalf("reasoning effort = %q, want %q",
			intent.ReasoningEffort, model.ReasoningMinimal)
	}
	if intent.ReasoningEnabled != nil {
		t.Fatalf("reasoning_enabled = %v, want unset beside an effort",
			*intent.ReasoningEnabled)
	}
	want := 4096 + condenseReasoningReserve
	if intent.MaxOutputTokens == nil || *intent.MaxOutputTokens != want {
		t.Fatalf("max output = %v, want the budget plus a reasoning reserve (%d)",
			intent.MaxOutputTokens, want)
	}
}

// TestCondenseRetriesWithTheObservedReasoningSpend is the regression for the
// failure that killed a real turn: the first attempt spends the whole cap
// thinking, so the retry has to cover that spend plus the prose.
func TestCondenseRetriesWithTheObservedReasoningSpend(t *testing.T) {
	var caps []int
	var calls int
	tool := &Tool{
		probe: func(context.Context, inference.GenerateRequest) condenseProbe {
			return condenseProbe{reasoningCanBeDisabled: true}
		},
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			calls++
			caps = append(caps, *req.Input.Content.Intent.Text.MaxOutputTokens)
			if calls == 1 {
				return reasoningOnlyResponse(1365), nil
			}
			return textResponse("S1"), nil
		},
	}
	out, err := tool.Execute(context.Background(), condenseArgs(t, tool, "s-1", 4096))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := patchSummary(t, out.Text()); got != "S1" {
		t.Fatalf("summary = %q, want the retry's text (S1)", got)
	}
	if len(caps) != 2 {
		t.Fatalf("generate calls = %d, want a single retry", len(caps))
	}
	want := 4096 + 1365 + condenseRetryMargin
	if caps[1] != want {
		t.Fatalf("retry cap = %d, want %d (budget + observed reasoning + margin)",
			caps[1], want)
	}
}

// TestCondenseFoldsAMechanicalDigestWhenNoModelSummaryArrives is the
// guarantee the whole ladder exists for: the fold still lands — the patch is
// marked, the covered ids are persisted — so the channel shrinks instead of
// the next round sending an over-window request.
func TestCondenseFoldsAMechanicalDigestWhenNoModelSummaryArrives(t *testing.T) {
	var calls int
	tool := &Tool{
		probe: func(context.Context, inference.GenerateRequest) condenseProbe {
			return condenseProbe{reasoningCanBeDisabled: true}
		},
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, error) {
			calls++
			return reasoningOnlyResponse(1365), nil
		},
	}
	out, err := tool.Execute(context.Background(), condenseArgs(t, tool, "s-1", 4096))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	summary := patchSummary(t, out.Text())
	if !strings.Contains(summary, "Folded rounds (mechanical digest)") {
		t.Fatalf("summary is not a mechanical digest:\n%s", summary)
	}
	if !strings.Contains(summary, `"m1"`) {
		t.Fatalf("digest dropped the folded ask:\n%s", summary)
	}
	if calls != 2 {
		t.Fatalf("generate calls = %d, want the retry before degrading", calls)
	}
}

// TestCondenseRespectsTheDeclaredOutputLimit keeps the adaptive cap inside
// what the deployment declares: a summary is worth paying for, an
// unbounded generation is not.
func TestCondenseRespectsTheDeclaredOutputLimit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		declared   int
		budget     int
		reasoning  bool
		wantOutput int
	}{
		{"declared limit wins", 3000, 4096, false, 3000},
		{"ceiling wins", 1000000, 20000, false, condenseOutputCeiling},
		{"floor applies", 0, 16, true, condenseMinOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := planCondense(
				condenseProbe{
					effortNative:           !tc.reasoning,
					reasoningCanBeDisabled: tc.reasoning,
					maxOutputTokens:        tc.declared,
				},
				tc.budget,
			).outputTokens()
			if got != tc.wantOutput {
				t.Fatalf("output tokens = %d, want %d", got, tc.wantOutput)
			}
		})
	}
}

// TestCondenseSkipsArtifactStateForEphemeralContexts covers a delegated
// subagent run: the session store rejects "ctx-" ids, so the fold must
// neither warn nor remember an artifact it cannot own.
