// Package core UI event DTOs shared by the desktopv2 bindings and the
// automation runner. Keeping the wire shapes next to Shell.Emit makes
// the frontend event contract explicit instead of ad-hoc maps.
package core

import (
	"time"

	"github.com/GizClaw/flowcraft/core/inference"
)

// TurnEndEvent is the terminal turn event consumed by the frontend.
type TurnEndEvent struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
	// Output is the run's final assistant text (bounded), used by
	// automation notifications outside the open workspace where the
	// frontend has no streamed transcript to build the snippet from.
	Output string `json:"output,omitempty"`
	// Notify lets an automation task suppress the system notification
	// for this turn (nil = notify, the default for user turns).
	Notify *bool `json:"notify,omitempty"`
}

// NewTurnEnd builds a wire turn-end event with RFC3339 time.
func NewTurnEnd(
	runID, conversationID, status, errorText, output string,
	finishedAt time.Time,
) TurnEndEvent {
	return TurnEndEvent{
		RunID:          runID,
		ConversationID: conversationID,
		Status:         status,
		Error:          errorText,
		FinishedAt:     finishedAt.UTC().Format(time.RFC3339),
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
	if usage.Model.ID.Provider != "" && usage.Model.ID.Name != "" {
		ev.Model = usage.Model.ID.Provider + "/" + usage.Model.ID.Name
	}
	return ev
}

// StatusEvent updates the status bar.
type StatusEvent struct {
	Text string `json:"text"`
	Busy bool   `json:"busy"`
}
