package core

import (
	"context"
	"sync"

	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// Prompt implements interact.Backend with an in-process pending
// registry so the Conversation binding can answer prompts directly.
type Prompt struct {
	mu      sync.Mutex
	pending map[string]chan interact.Reply
	notify  func(typ string, data any)
}

// NewPrompt creates the prompt backend.
func NewPrompt() *Prompt {
	return &Prompt{pending: make(map[string]chan interact.Reply)}
}

// SetNotifier installs a UI event emitter used while prompts are open.
func (p *Prompt) SetNotifier(fn func(typ string, data any)) {
	p.mu.Lock()
	p.notify = fn
	p.mu.Unlock()
}

// Ask registers one prompt and blocks for an answer.
func (p *Prompt) Ask(ctx context.Context, spec interact.Spec) (interact.Reply, error) {
	ch := make(chan interact.Reply, 1)
	p.mu.Lock()
	p.pending[spec.ID] = ch
	notify := p.notify
	p.mu.Unlock()
	if notify != nil {
		notify("interact", map[string]any{
			"id":          spec.ID,
			"run_id":      spec.RunID,
			"kind":        string(spec.Kind),
			"title":       spec.Title,
			"source":      spec.Source,
			"allow_other": spec.AllowOther,
		})
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
	ch, ok := p.pending[promptID]
	if ok {
		delete(p.pending, promptID)
	}
	p.mu.Unlock()
	if !ok {
		return false
	}
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
