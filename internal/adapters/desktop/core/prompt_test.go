package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// TestPromptAskEmitsCompleteInteractDTO verifies the interact event
// carries every field the frontend InteractionCard renders: the
// discriminated body parts, options and multi/allow_other flags.
func TestPromptAskEmitsCompleteInteractDTO(t *testing.T) {
	p := NewPrompt()
	conv := NewConversation()
	sessionID := conv.New("/tmp/w")
	conv.TrackRun(sessionID, "r-1")
	p.SetRunConvResolver(conv.ConversationForRun)
	events := make(chan map[string]any, 1)
	p.SetNotifier(func(_ string, data any) {
		events <- data.(map[string]any)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	spec := interact.Spec{
		ID:         "p-1",
		RunID:      "r-1",
		Kind:       interact.KindConfirm,
		Title:      "Allow running ls?",
		Body:       []message.Part{message.TextPart{Text: "run ls"}},
		Options:    []interact.Option{{Label: "Yes", Value: "yes"}, {Label: "No", Value: "no"}},
		Multi:      false,
		AllowOther: false,
		Source:     "test",
	}
	go func() {
		_, err := p.Ask(ctx, spec)
		done <- err
	}()

	select {
	case data := <-events:
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		var dto struct {
			ID             string `json:"id"`
			RunID          string `json:"run_id"`
			ConversationID string `json:"conversation_id"`
			Kind           string `json:"kind"`
			Title          string `json:"title"`
			Body           []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"body"`
			Options []struct {
				Label string `json:"label"`
				Value string `json:"value"`
			} `json:"options"`
			Multi      bool   `json:"multi"`
			AllowOther bool   `json:"allow_other"`
			Source     string `json:"source"`
		}
		if err := json.Unmarshal(raw, &dto); err != nil {
			t.Fatalf("decode event: %v\n%s", err, raw)
		}
		if dto.ID != "p-1" || dto.RunID != "r-1" || dto.Kind != "confirm" {
			t.Errorf("identity/kind = %+v", dto)
		}
		if dto.ConversationID != sessionID {
			t.Errorf("conversation_id = %q, want %q", dto.ConversationID, sessionID)
		}
		if len(dto.Body) != 1 ||
			dto.Body[0].Type != "text" ||
			dto.Body[0].Text != "run ls" {
			t.Errorf("body = %+v, want one text part", dto.Body)
		}
		if len(dto.Options) != 2 ||
			dto.Options[0].Label != "Yes" ||
			dto.Options[0].Value != "yes" {
			t.Errorf("options = %+v, want yes/no choices", dto.Options)
		}
		if dto.Multi || dto.AllowOther {
			t.Errorf("flags = multi:%v allow_other:%v, want both false", dto.Multi, dto.AllowOther)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interact event was not emitted")
	}

	if ok := p.Answer("p-1", "", "yes", nil, false); !ok {
		t.Fatal("Answer did not resolve pending prompt")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Ask returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ask did not return after answer")
	}
}

// TestPromptResolveEmitsResolvedEvent verifies that externally closed
// prompts (turn end, timeout, cancellation) emit a resolved event with
// the owning conversation id so the UI can drop the pending card.
func TestPromptResolveEmitsResolvedEvent(t *testing.T) {
	p := NewPrompt()
	conv := NewConversation()
	sessionID := conv.New("/tmp/w")
	conv.TrackRun(sessionID, "r-1")
	p.SetRunConvResolver(conv.ConversationForRun)
	events := make(chan map[string]any, 2)
	p.SetNotifier(func(_ string, data any) {
		events <- data.(map[string]any)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	spec := interact.Spec{
		ID:    "p-1",
		RunID: "r-1",
		Kind:  interact.KindText,
		Title: "Question",
	}
	go func() {
		_, err := p.Ask(ctx, spec)
		done <- err
	}()

	select {
	case <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("interact event was not emitted")
	}
	if err := p.Resolve(
		context.Background(), "p-1", coresession.PromptClosed, "turn ended",
	); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case data := <-events:
		var dto struct {
			ID             string `json:"id"`
			ConversationID string `json:"conversation_id"`
			Status         string `json:"status"`
			Reason         string `json:"reason"`
		}
		raw, _ := json.Marshal(data)
		if err := json.Unmarshal(raw, &dto); err != nil {
			t.Fatalf("decode resolved: %v", err)
		}
		if dto.ID != "p-1" ||
			dto.ConversationID != sessionID ||
			dto.Status != string(coresession.PromptClosed) ||
			dto.Reason != "turn ended" {
			t.Errorf("resolved = %+v, want p-1/%s/closed/turn ended", dto, sessionID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resolved event was not emitted")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Ask did not return after resolve")
	}
}
