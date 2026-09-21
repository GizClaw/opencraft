package host

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// maxSteerQueuedBytes bounds the steer payload one turn may hold
// queued at a time, measured in message-text bytes. Core already
// bounds one message (32 KiB) and the queue depth (8), and eight full
// messages drained at one boundary would inject a quarter of a
// megabyte into the conversation in a single round — inside the
// window automatic compaction has frozen for the turn. The cap keeps
// the worst case at what one steer may carry: a boundary that drains
// the queue merges it into one core-sized user message.
//
// A single message never meets the cap: core admits one message into
// an empty queue by its own size limit (32 KiB of JSON, so less than
// this much text), and the host does not second-guess that. What the
// cap bounds is the sum, so only a queue that is already holding
// something can refuse.
//
// A submit that would cross the cap is refused like any other rejected
// steer (the caller keeps the text and decides what to do with it),
// and the ledger below reconciles with the live queue on the next
// submit, so a boundary's drain frees the budget again right away.
const maxSteerQueuedBytes = 32 << 10

// ErrSteerQueueTooLarge reports a steer refused because the turn
// already holds the maximum steer payload queued. It is deliberately
// not core's ErrSteerTooLarge (the message itself is acceptable) and
// not ErrSteerQueueFull (there is room in the queue, just not for this
// much text): callers treat it as a rejection they may retry after the
// next round boundary.
var ErrSteerQueueTooLarge = errors.New("host: steer queue payload limit reached")

// steerPendingStateKey is the core session result-state key that
// records how many steered messages a settled turn ended without
// delivering. It is a key of the flowcraft session contract, not an
// OpenCraft state: core writes it from Turn.finish (recordPendingSteer
// in runtime/session/turn.go) only when the count is non-zero, which
// is why an absent key is a known zero here. PendingSteer is the one
// reader of that key, and TestPendingSteerReadsTheRealTurnResult pins
// both halves of the contract against a real turn — a renamed key
// fails that test instead of silently reporting "everything made it"
// for text the archive never saw.
const steerPendingStateKey = "session.pending_steer"

// PendingSteer reads the undelivered-steer count off a settled turn
// result: how many steered messages the turn ended without delivering.
// known is false when the count cannot be trusted — no result at all
// (the wait was cut short before the turn settled), or a value this
// build does not understand. Callers must treat that as "assume every
// steered message is undelivered": the transcript rows are the only
// copy of that text, and archive reconciliation rebuilds the turn from
// the archive, which never saw them.
func PendingSteer(res *agent.Result) (count int, known bool) {
	if res == nil {
		return 0, false
	}
	if res.State == nil {
		return 0, true
	}
	v, ok := res.State[steerPendingStateKey]
	if !ok {
		return 0, true
	}
	switch n := v.(type) {
	case int:
		if n >= 0 {
			return n, true
		}
	case float64:
		if n >= 0 && n == math.Trunc(n) && n <= math.MaxInt32 {
			return int(n), true
		}
	}
	return 0, false
}

// steerQueuedBytes reports the steer payload the run currently holds
// queued, reconciling the ledger with the live engine queue first: the
// graph's boundary drain (host.drainSteer in the steer node) removes
// the oldest queued messages without the host observing it, and core's
// PendingSteer is the only view of what is left. Draining is FIFO, so
// the messages still queued are the newest ones the ledger holds, and
// the ledger is only ever truncated toward the live count — never
// grown — so a stale ledger over-counts at worst. A submit that then
// sees a full queue is refused: early, never over the budget.
//
// The one exception is two submits racing for the last of the budget:
// the engine call is outside Host.mu, so neither sees the other's
// message and each may pass the check. The overshoot is bounded by one
// message, and the UI submits steers one at a time, so the cap is kept
// in practice; making it exact would hold Host.mu across the engine
// call for every submit.
//
// Callers hold Host.mu; the ledger field documents the same.
func (d *runDetail) steerQueuedBytes() int {
	if d == nil || d.run == nil || d.run.turn == nil {
		return 0
	}
	if queued := d.run.turn.PendingSteer(); queued < len(d.steerSizes) {
		d.steerSizes = d.steerSizes[len(d.steerSizes)-queued:]
	}
	total := 0
	for _, size := range d.steerSizes {
		total += size
	}
	return total
}

// SteerRun hands one mid-turn message to a live engine turn. The
// message is text-only and is delivered at the next round boundary the
// running document drains steer at (the assistant graph does so between
// a tool round and the next inference round), never in the middle of a
// streamed response. A rejected message leaves the turn running: the
// caller keeps the text and decides whether to interrupt, queue it for
// the next turn, or surface the rejection. Rejections include core's
// per-message and queue-length limits and the host's own payload
// budget ([ErrSteerQueueTooLarge]) on what one boundary may inject.
func (h *Host) SteerRun(runID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("host: steer message is empty")
	}
	size := len(text)
	h.mu.Lock()
	d := h.runs[RunID(runID)]
	if d == nil || d.run == nil || d.run.turn == nil {
		h.mu.Unlock()
		return errors.New("host: turn not found")
	}
	queued := d.steerQueuedBytes()
	// queued > 0 keeps a lone message the size core accepts from being
	// refused here: an empty queue takes whatever one message core
	// admits, and the sum is what the cap is about.
	if queued > 0 && queued+size > maxSteerQueuedBytes {
		h.mu.Unlock()
		return fmt.Errorf(
			"host: steer turn %s: %w (%d bytes queued, %d byte message)",
			runID, ErrSteerQueueTooLarge, queued, size)
	}
	turn := d.run.turn
	h.mu.Unlock()
	// The engine call runs outside Host.mu, so the ledger records only
	// what core accepted: a refusal here (closed turn, oversized
	// message, queue full) must not consume budget.
	if err := turn.Steer(
		message.NewTextMessage(message.RoleUser, text),
	); err != nil {
		return fmt.Errorf("host: steer turn %s: %w", runID, err)
	}
	h.mu.Lock()
	d.steerSizes = append(d.steerSizes, size)
	h.mu.Unlock()
	return nil
}
