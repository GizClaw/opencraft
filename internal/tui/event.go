package tui

import (
	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/interact"
)

// Event is one domain event delivered from the bridge layer to the UI.
type Event struct {
	Stream   *StreamEvent
	Status   *StatusEvent
	Usage    *UsageEvent
	Interact *InteractEvent
	Resolved *ResolvedEvent
}

// StreamEvent carries one runtime stream delta (token / tool call /
// tool result).
type StreamEvent struct {
	Delta agent.StreamDeltaPayload
}

// InteractEvent asks the user one question. The UI renders the spec and
// delivers the answer on ReplyCh.
type InteractEvent struct {
	Spec    interact.Spec
	ReplyCh chan interact.Reply
}

// ResolvedEvent notifies the UI that a pending interaction was resolved
// externally (expired / interrupted / closed) so its view can be
// invalidated without waiting for the whole turn to end.
type ResolvedEvent struct {
	ID     string
	Status session.PromptStatus
}

// StatusEvent updates the status bar.
type StatusEvent struct {
	Text string
	Busy bool
}

// UsageEvent reports one inference usage report plus the model that
// produced it ("provider/name").
type UsageEvent struct {
	Model            string
	InputTokens      int64
	OutputTokens     int64
	TotalTokens      int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	LatencyMs        int64
}

// batchMsg delivers one or more Events in a single tea message so the
// UI can keep up with high-throughput streams without backpressure.
type batchMsg struct {
	events []Event
}
