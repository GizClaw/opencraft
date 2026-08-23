package desktop

import (
	"context"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/runtime/session"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/GizClaw/opencraft/internal/runtime"
)

// Bridge converts runtime streams and prompt protocol events into
// UIEvent envelopes pushed to the frontend over Wails' event bus, and
// implements the interactive runtime.Backend for user questions. It
// replaces the TUI bridge: rendering moved to the web frontend, the
// contract (one event channel, pending prompt registry) is identical.
type Bridge struct {
	ctx context.Context

	mu      sync.Mutex
	pending map[string]chan runtime.Reply
}

// NewBridge creates the frontend bridge.
func NewBridge() *Bridge {
	return &Bridge{pending: make(map[string]chan runtime.Reply)}
}

// SetContext installs the Wails runtime context used for event
// emission. It is set during Startup, before the broker attaches.
func (b *Bridge) SetContext(ctx context.Context) {
	b.ctx = ctx
}

// Emit pushes one UI event to the frontend. Events emitted before the
// frontend subscribes are dropped; the UI pulls authoritative state
// (ConfigStatus) on mount and treats events as deltas.
func (b *Bridge) Emit(typ string, data any) {
	if b.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(b.ctx, "opencraft:ui", UIEvent{Type: typ, Data: data})
}

// Sink adapts a stream delta into a UI event. Output deltas are never
// dropped: the frontend must render every byte of the stream.
func (b *Bridge) Sink(
	ctx context.Context,
	_ event.Envelope,
	delta agent.StreamDeltaPayload,
) error {
	b.Emit("stream", delta)
	return nil
}

// Ask implements runtime.Backend: it registers the prompt for the
// frontend and blocks until an answer arrives or ctx ends.
func (b *Bridge) Ask(ctx context.Context, spec runtime.Spec) (runtime.Reply, error) {
	replyCh := make(chan runtime.Reply, 1)
	b.mu.Lock()
	b.pending[spec.ID] = replyCh
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, spec.ID)
		b.mu.Unlock()
	}()

	body := marshalParts(spec.Body)
	opts := make([]OptionDTO, 0, len(spec.Options))
	for _, o := range spec.Options {
		opts = append(opts, OptionDTO{Label: o.Label, Value: o.Value})
	}
	b.Emit("interact", InteractDTO{
		ID:         spec.ID,
		RunID:      spec.RunID,
		Kind:       string(spec.Kind),
		Title:      spec.Title,
		Body:       body,
		Options:    opts,
		Multi:      spec.Multi,
		AllowOther: spec.AllowOther,
		Source:     spec.Source,
	})

	select {
	case reply := <-replyCh:
		return reply, nil
	case <-ctx.Done():
		return runtime.Reply{}, ctx.Err()
	}
}

// Answer delivers one frontend reply to a pending prompt. It returns
// false when the prompt is no longer pending (already answered or
// resolved externally).
func (b *Bridge) Answer(promptID string, req ReplyRequest) (bool, error) {
	b.mu.Lock()
	ch, ok := b.pending[promptID]
	if ok {
		delete(b.pending, promptID)
	}
	b.mu.Unlock()
	if !ok {
		return false, nil
	}
	reply := runtime.Reply{
		ID:      promptID,
		Status:  runtime.ReplyOK,
		Text:    req.Text,
		Option:  req.Option,
		Options: req.Options,
	}
	if req.Cancel {
		reply.Status = runtime.ReplyCancelled
		reply.Text = ""
		reply.Option = nil
		reply.Options = nil
	}
	ch <- reply
	return true, nil
}

// Resolve implements runtime.Resolver: it notifies the frontend that a
// pending interaction was closed externally.
func (b *Bridge) Resolve(
	ctx context.Context,
	id string,
	status session.PromptStatus,
	reason string,
) error {
	b.mu.Lock()
	delete(b.pending, id)
	b.mu.Unlock()
	b.Emit("resolved", ResolvedDTO{
		ID: id, Status: string(status), Reason: reason,
	})
	return nil
}

// Status publishes a status annotation; cosmetic, so a not-yet-loaded
// frontend drops it rather than blocking the engine.
func (b *Bridge) Status(text string, busy bool) {
	b.Emit("status", StatusDTO{Text: text, Busy: busy})
}

// Usage publishes an inference usage report. It runs on the engine's
// goroutine and must be non-blocking.
func (b *Bridge) Usage(usage inference.Usage) {
	ev := UsageDTO{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
		LatencyMs:    usage.LatencyMs,
	}
	if usage.Output.ReasoningTokens != nil {
		ev.ReasoningTokens = *usage.Output.ReasoningTokens
	}
	if usage.Input.CacheReadTokens != nil {
		ev.CacheReadTokens = *usage.Input.CacheReadTokens
	}
	if usage.Input.CacheWriteTokens != nil {
		ev.CacheWriteTokens = *usage.Input.CacheWriteTokens
	}
	id := usage.Model.ID
	if id.Provider != "" && id.Name != "" {
		ev.Model = id.Provider + "/" + id.Name
	}
	b.Emit("usage", ev)
}

var _ runtime.Backend = (*Bridge)(nil)
var _ runtime.Resolver = (*Bridge)(nil)
