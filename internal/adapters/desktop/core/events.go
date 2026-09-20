// Package core UI event DTOs shared by the desktop bindings and the
// automation runner. Keeping the wire shapes next to Shell.Emit makes
// the frontend event contract explicit instead of ad-hoc maps.
package core

import (
	"time"

	"github.com/GizClaw/flowcraft/core/inference"
)

// AssistantAgentID is the flowcraft agent identity that desktop
// conversation and automation turns execute as. It is stamped onto UI
// events so consumers (pet activity feed, per-agent projections) can
// attribute work without re-deriving it from session keys.
const AssistantAgentID = "assistant"

// TurnEndEvent is the terminal turn event consumed by the frontend.
type TurnEndEvent struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	// AgentID identifies the agent that produced the run. Desktop UI
	// and automation turns both execute as AssistantAgentID today;
	// delegated subagent turns will carry their own id.
	AgentID string `json:"agent_id,omitempty"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	// InterruptCause / ErrorKind are the structured class of a failed
	// turn (an engine interrupt cause, an inference error kind, or the
	// harness-level timeout). The UI renders from these; Error stays for
	// correlation with provider logs. Both are empty when the turn did
	// not fail that way.
	InterruptCause string `json:"interrupt_cause,omitempty"`
	ErrorKind      string `json:"error_kind,omitempty"`
	// RequestID is the provider request identifier of the terminal
	// operation, when the provider reported one. It usually populates
	// failed turns (carried by the error chain).
	RequestID string `json:"request_id,omitempty"`
	// ResponseID is the provider response identifier (chat/message
	// id), when a response started. It usually populates successful
	// turns and may be the only correlation id available there.
	ResponseID string `json:"response_id,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	// DurationMs is the Host-measured run duration persisted with the
	// archived turn. It is always present (even when zero) so the UI
	// never has to estimate from second-precision timestamps.
	DurationMs int64 `json:"duration_ms"`
	// Output is the run's final assistant text (bounded), used by
	// automation notifications outside the open workspace where the
	// frontend has no streamed transcript to build the snippet from.
	Output string `json:"output,omitempty"`
	// Notify lets an automation task suppress the system notification
	// for this turn (nil = notify, the default for user turns).
	Notify *bool `json:"notify,omitempty"`
	// Compaction is what automatic context compaction did during this
	// turn, when the turn reached the compaction node. A fold rewrites the
	// conversation prefix, which invalidates the provider's prompt cache:
	// the turn that folded pays full input price on its next request, and
	// the UI says so once instead of leaving the user to wonder why a
	// familiar conversation suddenly cost more.
	Compaction *CompactionEvent `json:"compaction,omitempty"`
	// SteerPending is how many mid-turn steered messages the turn ended
	// without delivering. The count lives on the turn result state, not
	// in an event, because the run-end envelope is published before the
	// turn settles; the UI reads it to keep the optimistic steer rows it
	// drew from silently vanishing with archive reconciliation.
	SteerPending int `json:"steer_pending,omitempty"`
}

// CompactionEvent mirrors worldstate.CompactionReport onto the wire.
type CompactionEvent struct {
	// Folds is the number of successful folds in the turn.
	Folds int `json:"folds"`
	// Failures is the number of consecutive failed condensations standing
	// at the end of the turn.
	Failures int `json:"failures,omitempty"`
	// Notified reports whether the model was told that compaction is out
	// of options while the prompt is still over budget.
	Notified bool `json:"notified,omitempty"`
}

// NewTurnEnd builds a wire turn-end event with the RFC3339 end time
// and the duration the Host persisted for this run.
func NewTurnEnd(
	runID, conversationID, status, errorText, requestID, responseID string,
	output string,
	finishedAt time.Time, durationMs int64,
) TurnEndEvent {
	return TurnEndEvent{
		RunID:          runID,
		ConversationID: conversationID,
		Status:         status,
		Error:          errorText,
		RequestID:      requestID,
		ResponseID:     responseID,
		FinishedAt:     finishedAt.UTC().Format(time.RFC3339),
		DurationMs:     durationMs,
		Output:         output,
	}
}

// UsageEvent reports one inference usage report.
type UsageEvent struct {
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	ReasoningTokens  int64  `json:"reasoning_tokens"`
	LatencyMs        int64  `json:"latency_ms"`
}

// NewUsageEvent maps an inference usage report to the UI wire shape.
func NewUsageEvent(usage inference.Usage) UsageEvent {
	ev := UsageEvent{
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
	if usage.Model.ID.Name != "" {
		ev.Model = usage.Model.ID.Name
	}
	return ev
}

// StatusEvent updates the status bar.
type StatusEvent struct {
	Text string `json:"text"`
	Busy bool   `json:"busy"`
}
