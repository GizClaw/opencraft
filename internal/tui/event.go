package tui

import (
	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// Event is one domain event delivered from the bridge layer to the UI.
type Event struct {
	Stream  *StreamEvent
	Prompt  *PromptRequest
	Approve *ApproveRequest
	Status  *StatusEvent
}

// StreamEvent carries one runtime stream delta (token / tool call /
// tool result).
type StreamEvent struct {
	Delta agent.StreamDeltaPayload
}

// PromptRequest asks the user a question (ask_user).
type PromptRequest struct {
	Text    string
	ReplyCh chan agent.UserReply
}

// ApproveRequest asks the user to approve a tool call.
type ApproveRequest struct {
	Call message.ToolCall
	Done chan bool
}

// StatusEvent updates the status bar.
type StatusEvent struct {
	Text string
	Busy bool
}

// batchMsg delivers one or more Events in a single tea message so the
// UI can keep up with high-throughput streams without backpressure.
type batchMsg struct {
	events []Event
}

// modalResultMsg reports a modal outcome.
type modalResultMsg struct {
	reply    *agent.UserReply // ask
	approved *bool            // approve / confirm
}
