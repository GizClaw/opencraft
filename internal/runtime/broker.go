package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/runtime/session"
)

// Broker subscribes to the runtime's prompt events and routes each
// prompt to the registered Backend. Replies are delivered back through
// the turn that owns the prompt, which callers register with BindTurn
// while a turn is running.
type Broker struct {
	rt      Runtime
	backend Backend

	mu      sync.Mutex
	detach  []func()
	closed  bool
	turns   map[string]Replier
	reasons map[string]error // promptID -> Ask error, consumed by PromptResolved
}

// New creates a Broker over rt with one backend.
func New(rt Runtime, backend Backend) *Broker {
	return &Broker{
		rt:      rt,
		backend: backend,
		turns:   make(map[string]Replier),
	}
}

// BindTurn registers the turn that owns prompt replies. Callers must
// register before starting the turn's first AskUser and UnbindTurn when
// the turn ends.
func (b *Broker) BindTurn(turnID string, turn Replier) {
	b.mu.Lock()
	b.turns[turnID] = turn
	b.mu.Unlock()
}

// UnbindTurn drops the turn reference once it is no longer waiting on
// prompts.
func (b *Broker) UnbindTurn(turnID string) {
	b.mu.Lock()
	delete(b.turns, turnID)
	b.mu.Unlock()
}

// Attach subscribes to prompt-requested and prompt-resolved subjects
// on the runtime. The subscription lives until Close or the runtime is
// closed.
func (b *Broker) Attach(ctx context.Context) error {
	detachReq, err := b.rt.Attach(
		ctx, session.PatternPromptRequested(), event.SinkFunc(b.onPromptRequested))
	if err != nil {
		return err
	}
	detachRes, err := b.rt.Attach(
		ctx, session.PatternPromptResolved(), event.SinkFunc(b.onPromptResolved))
	if err != nil {
		detachReq()
		return err
	}
	b.mu.Lock()
	b.detach = append(b.detach, detachReq, detachRes)
	b.mu.Unlock()
	return nil
}

// Close detaches every subscription. It is idempotent.
func (b *Broker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	detach := b.detach
	b.detach = nil
	b.mu.Unlock()
	for _, d := range detach {
		d()
	}
}

func (b *Broker) onPromptRequested(ctx context.Context, env event.Envelope) error {
	req, err := decodePromptRequested(env)
	if err != nil {
		return err
	}
	spec := FromPrompt(req.Prompt, req.PromptID, req.RunID, req.TurnID)
	go b.ask(ctx, spec)
	return nil
}

// wirePromptRequested mirrors session.PromptRequested with the prompt
// parts kept raw, so they can be reconstructed despite core v0.1.5
// marshalling []message.Part without a type discriminator.
type wirePromptRequested struct {
	RunID    string         `json:"run_id"`
	TurnID   string         `json:"turn_id"`
	PromptID string         `json:"prompt_id"`
	Prompt   wireUserPrompt `json:"prompt"`
}

type wireUserPrompt struct {
	Parts    []json.RawMessage `json:"parts"`
	Schema   []byte            `json:"schema"`
	Source   string            `json:"source"`
	Metadata map[string]string `json:"metadata"`
}

// decodePromptRequested decodes one PromptRequested envelope. A strict
// decode wins when the payload already uses the discriminated Content
// wire format; otherwise parts are reconstructed heuristically from
// the plain part objects core v0.1.5 produces. Unknown part shapes are
// dropped rather than failing the whole prompt.
func decodePromptRequested(env event.Envelope) (session.PromptRequested, error) {
	var req session.PromptRequested
	if err := env.Decode(&req); err == nil {
		return req, nil
	}
	var wire wirePromptRequested
	if err := env.Decode(&wire); err != nil {
		return session.PromptRequested{}, err
	}
	parts := make([]message.Part, 0, len(wire.Prompt.Parts))
	for _, raw := range wire.Prompt.Parts {
		if part, err := message.UnmarshalPart(raw); err == nil {
			parts = append(parts, part)
			continue
		}
		var text struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &text) == nil {
			parts = append(parts, message.TextPart{Text: text.Text})
		}
	}
	return session.PromptRequested{
		RunID:    wire.RunID,
		TurnID:   wire.TurnID,
		PromptID: wire.PromptID,
		Prompt: agent.UserPrompt{
			Parts:    parts,
			Schema:   wire.Prompt.Schema,
			Source:   wire.Prompt.Source,
			Metadata: wire.Prompt.Metadata,
		},
	}, nil
}

func (b *Broker) ask(ctx context.Context, spec Spec) {
	reply, err := b.backend.Ask(ctx, spec)
	if err != nil {
		// The UI is unavailable (or cancelled); resolve the prompt
		// empty so the agent does not block forever. Failures are
		// best-effort: the runtime prompt state machine still owns the
		// final outcome. Remember why Ask failed so the subsequent
		// PromptResolved can label the UI marker with the reason.
		b.mu.Lock()
		if b.reasons == nil {
			b.reasons = make(map[string]error)
		}
		b.reasons[spec.ID] = err
		b.mu.Unlock()
		reply = Reply{ID: spec.ID, Status: ReplyCancelled}
	}
	b.deliver(spec, reply)
}

// resolutionReason returns the reason attached to one prompt
// resolution, consuming the recorded Ask error. When no Ask error was
// observed yet (or ever, e.g. a cooperative interrupt), it falls back
// to a label derived from the status so the marker is never bare.
func (b *Broker) resolutionReason(id string, status session.PromptStatus) string {
	b.mu.Lock()
	err := b.reasons[id]
	delete(b.reasons, id)
	b.mu.Unlock()
	if err != nil {
		return promptReason(err, status)
	}
	return fallbackPromptReason(status)
}

// promptReason maps an Ask failure to a short display label. The
// status arrives independently via PromptResolved, so some labels
// depend on it: a cancelled context means "turn ended" when the prompt
// closed with the turn, but "cancelled" when it merely expired.
func promptReason(err error, status session.PromptStatus) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		if status == session.PromptClosed {
			return "turn ended"
		}
		return "cancelled"
	case errdefs.IsInterrupted(err):
		var intr agent.InterruptedError
		if errors.As(err, &intr) {
			if intr.Detail != "" {
				return intr.Detail
			}
			if intr.Cause != "" && intr.Cause != agent.CauseUnknown {
				return string(intr.Cause)
			}
		}
		return "interrupted"
	default:
		msg := err.Error()
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		return msg
	}
}

// fallbackPromptReason labels a resolution whose Ask outcome was not
// observable at this layer (the PromptResolved envelope won the race,
// or the interruption bypassed the Ask path entirely).
func fallbackPromptReason(status session.PromptStatus) string {
	switch status {
	case session.PromptExpired:
		return "context ended"
	case session.PromptClosed:
		return "turn ended"
	default:
		return ""
	}
}

func (b *Broker) deliver(spec Spec, reply Reply) {
	b.mu.Lock()
	turn := b.turns[spec.TurnID]
	b.mu.Unlock()
	if turn == nil {
		return
	}
	reply.ID = spec.ID
	err := turn.Reply(context.Background(), spec.ID, ToUserReply(reply))
	if err == nil {
		// A successful reply resolved the prompt; no PromptResolved
		// will follow, so drop any recorded Ask-error reason.
		b.mu.Lock()
		delete(b.reasons, spec.ID)
		b.mu.Unlock()
	}
}

func (b *Broker) onPromptResolved(ctx context.Context, env event.Envelope) error {
	var res session.PromptResolved
	if err := env.Decode(&res); err != nil {
		return err
	}
	if r, ok := b.backend.(Resolver); ok {
		_ = r.Resolve(ctx, res.PromptID, res.Status,
			b.resolutionReason(res.PromptID, res.Status))
	} else {
		b.resolutionReason(res.PromptID, res.Status)
	}
	return nil
}
