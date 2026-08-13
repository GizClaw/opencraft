package tui

import (
	"context"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
)

// Bridge converts external sources (runtime stream, user prompts,
// approvals) into domain Events on a channel the UI consumes. It also
// implements agent.UserPrompter and the approval callback, blocking
// until the UI delivers a result.
type Bridge struct {
	events chan Event

	mu      sync.RWMutex
	started bool
}

// NewBridge creates a bridge with the given event buffer.
func NewBridge(buffer int) *Bridge {
	if buffer <= 0 {
		buffer = 64
	}
	return &Bridge{events: make(chan Event, buffer)}
}

// Events returns the domain event channel.
func (b *Bridge) Events() <-chan Event {
	return b.events
}

// Start marks the bridge as consumed (the UI read loop is running).
func (b *Bridge) Start() {
	b.mu.Lock()
	b.started = true
	b.mu.Unlock()
}

// Sink adapts a stream delta into a domain event.
func (b *Bridge) Sink(
	_ context.Context,
	_ event.Envelope,
	delta agent.StreamDeltaPayload,
) error {
	b.events <- Event{Stream: &StreamEvent{Delta: delta}}
	return nil
}

// AskUser implements agent.UserPrompter.
func (b *Bridge) AskUser(
	ctx context.Context,
	prompt agent.UserPrompt,
) (agent.UserReply, error) {
	replyCh := make(chan agent.UserReply, 1)
	b.events <- Event{Prompt: &PromptRequest{
		Text:    promptText(prompt),
		ReplyCh: replyCh,
	}}
	select {
	case reply := <-replyCh:
		return reply, nil
	case <-ctx.Done():
		return agent.UserReply{}, ctx.Err()
	}
}

// Approve asks the user to approve one tool call.
func (b *Bridge) Approve(
	ctx context.Context,
	call message.ToolCall,
) error {
	done := make(chan bool, 1)
	b.events <- Event{Approve: &ApproveRequest{Call: call, Done: done}}
	select {
	case approved := <-done:
		if !approved {
			return errdefs.Forbiddenf(
				"tui: user rejected %s", call.Name)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Status publishes a status event.
func (b *Bridge) Status(text string, busy bool) {
	b.events <- Event{Status: &StatusEvent{Text: text, Busy: busy}}
}

func promptText(prompt agent.UserPrompt) string {
	for _, part := range prompt.Parts {
		if part.Kind() == message.PartText {
			if text, ok := part.(message.TextPart); ok {
				return text.Text
			}
		}
	}
	return "Question from the model"
}
