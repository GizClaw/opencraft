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
	// autoApprove, when set, answers a spec before it reaches the UI:
	// a non-empty option value short-circuits Ask (no pending entry,
	// no interact event) and becomes the reply, so the asking turn
	// sees an immediate answer (see SetAutoApprover).
	autoApprove func(ctx context.Context, spec interact.Spec, conversationID string) (string, bool)
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

// SetAutoApprover installs the predicate that answers a spec without
// presenting it. It runs before the prompt is registered or emitted;
// returning an option value with true means the caller of Ask gets an
// immediate "ok" reply carrying that value and the frontend never
// sees an interaction. A false return, an empty value (or no approver
// installed) keeps the interactive flow.
func (p *Prompt) SetAutoApprover(
	fn func(ctx context.Context, spec interact.Spec, conversationID string) (string, bool),
) {
	p.mu.Lock()
	p.autoApprove = fn
	p.mu.Unlock()
}

// Ask registers one prompt and blocks for an answer, unless an
// installed auto-approver answers it first.
func (p *Prompt) Ask(ctx context.Context, spec interact.Spec) (interact.Reply, error) {
	p.mu.Lock()
	conversationID := ""
	if p.runConv != nil {
		conversationID = p.runConv(spec.RunID)
	}
	approve := p.autoApprove
	p.mu.Unlock()
	if approve != nil {
		if option, ok := approve(ctx, spec, conversationID); ok && option != "" {
			// The predicate names the value, so an auto-answered
			// prompt carries the same option a click would (the
			// confirm gate's is confirm.OptionYes): the asking tool
			// sees a reply, not a special case.
			return interact.Reply{
				ID:     spec.ID,
				Status: interact.ReplyOK,
				Option: &option,
			}, nil
		}
	}
	ch := make(chan interact.Reply, 1)
	p.mu.Lock()
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
			"severity":    string(spec.Severity),
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
		notify(EventInteract, payload)
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
		notify(EventResolved, payload)
	}
	return nil
}

var _ interact.Resolver = (*Prompt)(nil)
