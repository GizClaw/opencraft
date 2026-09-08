package core

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/GizClaw/flowcraft/core/message"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// pendingPrompt keeps the reply channel plus the owning conversation
// captured when Ask registered the prompt. Resolve uses it so the
// frontend can route "resolved" without a pending-interact scan.
type pendingPrompt struct {
	ch             chan interact.Reply
	conversationID string
}

// Prompt implements interact.Backend with an in-process pending
// registry so the Conversation binding can answer prompts directly.
type Prompt struct {
	mu      sync.Mutex
	pending map[string]pendingPrompt
	notify  func(typ string, data any)
	runConv func(runID string) string
}

// NewPrompt creates the prompt backend.
func NewPrompt() *Prompt {
	return &Prompt{pending: make(map[string]pendingPrompt)}
}

// SetNotifier installs a UI event emitter used while prompts are open.
func (p *Prompt) SetNotifier(fn func(typ string, data any)) {
	p.mu.Lock()
	p.notify = fn
	p.mu.Unlock()
}

// SetRunConvResolver installs a run-id → conversation resolver so the
// interact event carries the owning conversation, matching the legacy
// Bridge contract.
func (p *Prompt) SetRunConvResolver(fn func(runID string) string) {
	p.mu.Lock()
	p.runConv = fn
	p.mu.Unlock()
}

// Ask registers one prompt and blocks for an answer.
func (p *Prompt) Ask(ctx context.Context, spec interact.Spec) (interact.Reply, error) {
	ch := make(chan interact.Reply, 1)
	p.mu.Lock()
	conversationID := ""
	if p.runConv != nil {
		conversationID = p.runConv(spec.RunID)
	}
	p.pending[spec.ID] = pendingPrompt{
		ch:             ch,
		conversationID: conversationID,
	}
	notify := p.notify
	p.mu.Unlock()
	if notify != nil {
		body := make([]json.RawMessage, 0, len(spec.Body))
		for _, part := range spec.Body {
			if raw, err := message.MarshalPart(part); err == nil {
				body = append(body, raw)
			}
		}
		options := spec.Options
		if options == nil {
			options = []interact.Option{}
		}
		payload := map[string]any{
			"id":          spec.ID,
			"run_id":      spec.RunID,
			"kind":        string(spec.Kind),
			"title":       spec.Title,
			"body":        body,
			"options":     options,
			"multi":       spec.Multi,
			"source":      spec.Source,
			"allow_other": spec.AllowOther,
		}
		if conversationID != "" {
			payload["conversation_id"] = conversationID
		}
		notify("interact", payload)
	}
	defer func() {
		p.mu.Lock()
		delete(p.pending, spec.ID)
		p.mu.Unlock()
	}()
	select {
	case reply := <-ch:
		return reply, nil
	case <-ctx.Done():
		return interact.Reply{}, ctx.Err()
	}
}

// Answer delivers one reply to a pending prompt.
func (p *Prompt) Answer(
	promptID, text, option string,
	options []string, cancel bool,
) bool {
	p.mu.Lock()
	prompt, ok := p.pending[promptID]
	if ok {
		delete(p.pending, promptID)
	}
	p.mu.Unlock()
	if !ok {
		return false
	}
	ch := prompt.ch
	reply := interact.Reply{ID: promptID, Status: interact.ReplyOK, Text: text, Options: options}
	if option != "" {
		reply.Option = &option
	}
	if cancel {
		reply.Status = interact.ReplyCancelled
		reply.Text = ""
		reply.Option = nil
		reply.Options = nil
	}
	ch <- reply
	return true
}

// Resolve implements interact.Resolver: it notifies the frontend that
// a pending interaction was closed externally.
func (p *Prompt) Resolve(
	ctx context.Context,
	id string,
	status coresession.PromptStatus,
	reason string,
) error {
	p.mu.Lock()
	prompt, ok := p.pending[id]
	delete(p.pending, id)
	notify := p.notify
	p.mu.Unlock()
	if notify != nil {
		payload := map[string]any{
			"id":     id,
			"status": string(status),
			"reason": reason,
		}
		if ok && prompt.conversationID != "" {
			payload["conversation_id"] = prompt.conversationID
		}
		notify("resolved", payload)
	}
	return nil
}

var _ interact.Resolver = (*Prompt)(nil)
