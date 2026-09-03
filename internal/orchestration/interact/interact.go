package interact

import (
	"github.com/GizClaw/flowcraft/core/agent"

	protocol "github.com/GizClaw/opencraft/internal/foundation/interact"
)

// The orchestration Broker keeps the orchestration-facing alias of the
// prompt protocol. Capability packages import the canonical protocol
// from foundation/interact, so they never depend on orchestration.

type Kind = protocol.Kind

const (
	KindText    = protocol.KindText
	KindConfirm = protocol.KindConfirm
	KindSelect  = protocol.KindSelect
)

type Option = protocol.Option
type Spec = protocol.Spec
type ReplyStatus = protocol.ReplyStatus

const (
	ReplyOK        = protocol.ReplyOK
	ReplyCancelled = protocol.ReplyCancelled
)

type Reply = protocol.Reply
type Backend = protocol.Backend
type Resolver = protocol.Resolver
type Runtime = protocol.Runtime
type Replier = protocol.Replier

const (
	MetaKind       = protocol.MetaKind
	MetaTitle      = protocol.MetaTitle
	MetaOptions    = protocol.MetaOptions
	MetaMulti      = protocol.MetaMulti
	MetaAllowOther = protocol.MetaAllowOther
	MetaChoice     = protocol.MetaChoice
	MetaChoices    = protocol.MetaChoices
	MetaOther      = protocol.MetaOther
	MetaStatus     = protocol.MetaStatus
)

// FromPrompt maps a core UserPrompt into a Spec.
func FromPrompt(p agent.UserPrompt, id, runID, turnID string) Spec {
	return protocol.FromPrompt(p, id, runID, turnID)
}

// ToUserReply converts a Reply back into the core UserReply shape.
func ToUserReply(r Reply) agent.UserReply {
	return protocol.ToUserReply(r)
}
