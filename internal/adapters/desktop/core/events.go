// Package core UI event DTOs shared by the desktop bindings and the
// automation runner. Keeping the wire shapes next to Shell.Emit makes
// the frontend event contract explicit instead of ad-hoc maps.
package core

import (
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
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
	// Workspace is the workspace whose Host ran the turn. A window can
	// be looking at a different one by the time the turn ends: the
	// sidebar has to file the row under its own workspace, and the
	// banner's click has to open the conversation there instead of
	// wherever the window happens to be.
	Workspace string `json:"workspace,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
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
	// Context is the prompt this turn's last model call carried, split by
	// what filled it — the meter the composer draws. It rides the
	// terminal event instead of being fetched on demand because the
	// numbers come off the turn's own board, which is only reachable
	// while the run result is still in hand; the persisted copy is what a
	// re-opened conversation reads (sessions.DocumentContextUsage).
	Context *ContextUsageEvent `json:"context,omitempty"`
	// Compaction is what automatic context compaction did during this
	// turn, when the turn reached the compaction node. A fold rewrites the
	// conversation prefix, which invalidates the provider's prompt cache:
	// the turn that folded pays full input price on its next request, and
	// the UI says so once instead of leaving the user to wonder why a
	// familiar conversation suddenly cost more.
	Compaction *CompactionEvent `json:"compaction,omitempty"`
	// SteerPending is how many mid-turn steered messages the turn ended
	// without delivering, or null when the count could not be read. The
	// count lives on the turn result state, not in an event, because the
	// run-end envelope is published before the turn settles.
	//
	// It is a pointer (like Notify) because the two states must stay
	// distinct on the wire: a turn that delivered everything sends a
	// literal 0, and a reader that sees null — or no field at all, from
	// a producer this build does not know — keeps every steered row
	// instead of trusting a zero. The transcript rows are the only copy
	// of that text, and archive reconciliation rebuilds the turn from
	// the archive, which never saw them.
	SteerPending *int `json:"steer_pending"`
}

// SteerPendingEvent reports how many mid-turn steered messages a run
// still has waiting for a round boundary — the live counterpart of the
// count turn_end carries, emitted whenever a boundary takes messages out
// of the queue. It is what lets the transcript flip an interjection note
// to delivered while the turn is still running; turn_end stays the
// authority on what never made it.
type SteerPendingEvent struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	// SteerPending is the number of steered messages still waiting for
	// the run's next boundary. Zero means a boundary has taken every
	// interjection the run was handed.
	SteerPending int `json:"steer_pending"`
}

// OpenConversationEvent asks the window to show one conversation. It is
// an intent, not a change of state: the only producer is the desktop
// shell, when the user activates a system notification, and the window
// answers by switching to the conversation the way a click in the
// sidebar would.
type OpenConversationEvent struct {
	ConversationID string `json:"conversation_id"`
	// Workspace is the workspace whose Host ran the conversation. The
	// window switches to it when it differs from the current one; an
	// empty value means the producer could not name it, and the window
	// stays where it is.
	Workspace string `json:"workspace,omitempty"`
}

// ContextCategoryEvent is one slice of the prompt, by category id. The
// ids are sessions.ContextCategories: the UI keeps its own label table, so
// an id this build does not know is a row it does not draw.
type ContextCategoryEvent struct {
	ID     string `json:"id"`
	Tokens int    `json:"tokens"`
}

// ContextUsageEvent mirrors sessions.ContextUsage onto the wire.
type ContextUsageEvent struct {
	// MeasuredTokens is the provider's count of the prompt; zero when the
	// call reported no usage, in which case the categories are estimates.
	MeasuredTokens int64 `json:"measured_tokens"`
	// Categories always sums to what the meter shows as used: the
	// measurement when there is one (the conversation and residual
	// categories absorbing the difference), the estimates otherwise.
	Categories []ContextCategoryEvent `json:"categories"`
	// Model is the model that served the measured call.
	Model string `json:"model,omitempty"`
	// LimitTokens is that model's input window, zero when the deployment
	// declares none and the catalog cannot say. A meter with no window
	// shows the absolute count and no percentage.
	LimitTokens int `json:"limit_tokens,omitempty"`
	// ConversationCapped says the conversation row is a remainder of the
	// measurement rather than a size of its own (see
	// sessions.ContextUsage.Normalize): the prompt carried less of the
	// conversation than prepare estimated, which is what a turn that
	// folded it mid-way does. The total is still the provider's count,
	// and the popover says so instead of drawing that row as an account
	// of what the conversation cost.
	ConversationCapped bool   `json:"conversation_capped,omitempty"`
	At                 string `json:"at,omitempty"`
}

// NewContextUsageEvent maps a persisted context snapshot to the wire shape.
func NewContextUsageEvent(
	usage ocsessions.ContextUsage, limit int,
) ContextUsageEvent {
	ev := ContextUsageEvent{
		MeasuredTokens:     usage.MeasuredTokens,
		Model:              usage.Model,
		LimitTokens:        limit,
		ConversationCapped: usage.ConversationCapped,
	}
	if !usage.At.IsZero() {
		ev.At = usage.At.UTC().Format(time.RFC3339)
	}
	if len(usage.Categories) > 0 {
		ev.Categories = make([]ContextCategoryEvent, 0, len(usage.Categories))
		for _, c := range usage.Categories {
			ev.Categories = append(ev.Categories, ContextCategoryEvent{
				ID:     c.ID,
				Tokens: c.Tokens,
			})
		}
	}
	return ev
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
// and the duration the Host persisted for this run. res is the settled
// turn result the event describes: reading the undelivered-steer count
// off it here (host.PendingSteer) is what keeps every turn_end
// producer reporting the same shape — a producer that has no result
// (the wait was cut short) leaves the count null, which the UI treats
// as unknown rather than as "everything made it".
func NewTurnEnd(
	runID, conversationID, status, errorText, requestID, responseID string,
	output string,
	finishedAt time.Time, durationMs int64,
	res *agent.Result,
) TurnEndEvent {
	end := TurnEndEvent{
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
	if count, known := host.PendingSteer(res); known {
		end.SteerPending = &count
	}
	return end
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
