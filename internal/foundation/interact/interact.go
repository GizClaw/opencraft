// Package interact defines the prompt protocol shared by capability
// tools and the orchestration Broker. It is intentionally independent
// of orchestration so capabilities can build user-facing prompts
// without depending on the Broker layer.
package interact

import (
	"context"
	"encoding/json"
	"maps"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/runtime/session"
	"github.com/GizClaw/flowcraft/core/telemetry"
)

// Kind describes the shape of one user interaction.
type Kind string

const (
	KindText    Kind = "text"
	KindConfirm Kind = "confirm"
	KindSelect  Kind = "select"
)

// Option is one selectable choice in a select/confirm interaction.
type Option struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Spec describes one question presented to the user. It is a UI
// rendering contract: same Spec, different Backend renderers.
type Spec struct {
	ID         string
	RunID      string
	TurnID     string
	Kind       Kind
	Title      string
	Body       []message.Part
	Options    []Option
	Multi      bool // select: allow picking several options
	AllowOther bool // select: include a free-form "other" choice
	Source     string
	Metadata   map[string]string
}

// ReplyStatus is the terminal status of one interaction.
type ReplyStatus string

const (
	ReplyOK        ReplyStatus = "ok"
	ReplyCancelled ReplyStatus = "cancelled"
)

// Reply is the user's answer to one Spec.
type Reply struct {
	ID       string
	Status   ReplyStatus
	Text     string         // free-form text (text kind, or select custom input)
	Option   *string        // selected option value (confirm/select)
	Options  []string       // selected option values (multi-select)
	Parts    []message.Part // raw answer parts, when the UI produced them
	Metadata map[string]string
}

// Backend presents Specs to the user and returns their Reply. Ask must
// observe ctx cancellation and return promptly when it fires.
type Backend interface {
	Ask(ctx context.Context, spec Spec) (Reply, error)
}

// Resolver is an optional Backend capability: it is notified when a
// pending prompt was resolved externally (expired / interrupted /
// closed) so the UI can close or invalidate the rendered interaction
// instead of waiting for the whole turn to end.
type Resolver interface {
	// Resolve reports one prompt resolution. reason is a short
	// human-readable label (e.g. "timeout", "turn ended") when the
	// runtime could observe why the prompt stopped waiting; it is
	// empty when the cause is not observable at this layer.
	Resolve(ctx context.Context, id string, status session.PromptStatus, reason string) error
}

// Runtime is the runtime surface the Broker needs. *runtime.Runtime
// satisfies it.
type Runtime interface {
	Attach(ctx context.Context, pattern event.Pattern, sink event.Sink, opts ...event.AttachOption) (func(), error)
}

// Replier delivers one prompt answer back to the runtime.
// *sessions.Turn satisfies it.
type Replier interface {
	Reply(ctx context.Context, promptID string, reply agent.UserReply) error
}

// Metadata keys understood when building a Spec from a core UserPrompt.
const (
	MetaKind       = "opencraft.interact.kind"
	MetaTitle      = "opencraft.interact.title"
	MetaOptions    = "opencraft.interact.options"     // JSON array of Option
	MetaMulti      = "opencraft.interact.multi"       // "true" enables multi-select
	MetaAllowOther = "opencraft.interact.allow_other" // "false" hides the other input
)

// Reply metadata produced when converting a Reply back to core.
const (
	MetaChoice  = "opencraft.interact.choice"
	MetaChoices = "opencraft.interact.choices" // JSON array (multi-select)
	MetaOther   = "opencraft.interact.other"
	MetaStatus  = "opencraft.interact.status"
)

// FromPrompt maps a core UserPrompt into a Spec. The kind, title, and
// options ride the opencraft metadata convention; anything else falls
// back to a text interaction with the prompt's first text part.
func FromPrompt(p agent.UserPrompt, id, runID, turnID string) Spec {
	meta := p.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	spec := Spec{
		ID:       id,
		RunID:    runID,
		TurnID:   turnID,
		Body:     p.Parts,
		Source:   p.Source,
		Metadata: cloneMeta(meta),
	}
	spec.Kind = KindText
	switch Kind(meta[MetaKind]) {
	case KindConfirm, KindSelect:
		spec.Kind = Kind(meta[MetaKind])
	}
	spec.Multi = spec.Kind == KindSelect && meta[MetaMulti] == "true"
	spec.AllowOther = spec.Kind == KindSelect && meta[MetaAllowOther] != "false"
	spec.Title = meta[MetaTitle]
	if spec.Title == "" {
		spec.Title = PromptText(p)
	}
	if raw := meta[MetaOptions]; raw != "" {
		var opts []Option
		if json.Unmarshal([]byte(raw), &opts) == nil {
			spec.Options = opts
		}
	}
	return spec
}

// ToUserReply converts a Reply back into the core UserReply shape,
// preserving raw parts when present and encoding the choice/status in
// metadata otherwise.
func ToUserReply(r Reply) agent.UserReply {
	meta := map[string]string{MetaStatus: string(r.Status)}
	maps.Copy(meta, r.Metadata)
	if r.Option != nil {
		meta[MetaChoice] = *r.Option
	}
	if len(r.Options) > 0 {
		raw, err := json.Marshal(r.Options)
		if err != nil {
			telemetry.WarnErr(context.Background(),
				"interact: marshal reply options failed", err)
		}
		meta[MetaChoices] = string(raw)
	}
	if r.Text != "" {
		meta[MetaOther] = r.Text
	}
	parts := r.Parts
	if len(parts) == 0 {
		switch {
		case r.Text != "":
			parts = []message.Part{message.TextPart{Text: r.Text}}
		case r.Option != nil && *r.Option != "" && len(r.Options) == 0:
			parts = []message.Part{message.TextPart{Text: *r.Option}}
		}
	}
	return agent.UserReply{Parts: parts, Metadata: meta}
}

// PromptText returns the first text part of a UserPrompt, or "".
func PromptText(p agent.UserPrompt) string {
	return PartsText(p.Parts)
}

// PartsText returns the concatenated text parts of a part slice.
func PartsText(parts []message.Part) string {
	var b strings.Builder
	for _, part := range parts {
		if text, ok := part.(message.TextPart); ok {
			b.WriteString(text.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

func cloneMeta(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
