package worldstate

import (
	"context"
	"encoding/json"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"go.opentelemetry.io/otel/attribute"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/ids"
)

// The board-var contract between this package, the graph's compaction node
// and the assistant graph definition:
//
//   - llm_usage: the last inference call's usage, written by the graph's
//     llm node (config key usage_key). The compaction node reads it, and
//     the turn-end hook here turns it into the persisted anchor.
//   - world.compact.anchor_len: the MainChannel length the compaction node
//     last sent to the llm node. Together with llm_usage it describes
//     exactly one request.
//   - world.compact.epoch_total: the fold generation a measurement belongs
//     to — the conversation's applied folds as of the last recorded
//     measurement (derived from the stored anchor, so a turn that folded
//     without writing one leaves it short). It is diagnostic: what decides
//     comparability is the anchor coverage check, not this number.
//   - world.compact.folds_turn: successful folds in this turn, which is
//     what the UI reports (the generation above is cumulative).
//   - world.compact.count: folds attempted (success or failure) this turn.
//   - world.usage.anchor: the previous turn's measurement, injected by the
//     prepare hook for the compaction node — full-replay deployments only,
//     see usageAnchorBoardValue.

// boardInt reads an integer board var, accepting the encodings both the
// script bridge and Go-side nodes produce.
func boardInt(board *agent.Board, key string) (int, bool) {
	if board == nil {
		return 0, false
	}
	v, _ := board.Var(key)
	switch v := v.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	default:
		return 0, false
	}
}

// boardUsage reads the last call's usage off the board. The llm node
// writes the Go value; a board restored from JSON carries the same fields
// as a generic value, so both shapes are accepted.
func boardUsage(board *agent.Board) (inference.Usage, bool) {
	if board == nil {
		return inference.Usage{}, false
	}
	raw, _ := board.Var(config.BoardVarLLMUsage)
	switch v := raw.(type) {
	case inference.Usage:
		return v, v.InputTokens > 0
	case *inference.Usage:
		if v == nil {
			return inference.Usage{}, false
		}
		return *v, v.InputTokens > 0
	case string:
		var usage inference.Usage
		if err := json.Unmarshal([]byte(v), &usage); err != nil {
			return inference.Usage{}, false
		}
		return usage, usage.InputTokens > 0
	default:
		return inference.Usage{}, false
	}
}

// usageAnchorBoardValue renders the persisted anchor for the compaction
// node, or returns ok=false when the conversation has no usable
// measurement yet. Errors are non-fatal by design: a missing anchor costs
// estimation accuracy, never a turn.
//
// It is exposed only to deployments that replay the full conversation
// (memory.replay_full_history). The node's only way to check an anchor is
// "does this channel still contain the measured prefix?" — a message count
// against a channel length — and that check is sound exactly while the
// prefix grows. A bounded memory window slides instead: the same index then
// covers different messages, the check passes anyway, and the node would
// take a measurement of one prompt as the floor for another (folding a
// conversation that fits, and paying a summarization call for it). Windowed
// deployments lose nothing they relied on: their first round is sized by
// the character estimate, and from the second round on the in-turn anchor
// (llm_usage + world.compact.anchor_len) is an exact measurement.
func (s *Service) usageAnchorBoardValue(
	ctx context.Context, contextID string,
) ([]byte, bool) {
	if s == nil || s.sessionStore == nil || contextID == "" {
		return nil, false
	}
	// Delegated subagent runs mint ephemeral "ctx-" ids the store rejects
	// (recordUsageAnchor guards the write side the same way): there is no
	// anchor to read, and asking would warn on every subagent turn.
	if !ids.IsSession(contextID) {
		return nil, false
	}
	if !s.replaysFullHistory() {
		return nil, false
	}
	anchor, err := s.sessionStore.ReadUsageAnchor(contextID)
	if err != nil {
		// An unreadable anchor was reported by the store; a missing one
		// just means there is no measurement yet.
		return nil, false
	}
	if !anchor.Valid() {
		return nil, false
	}
	data, err := json.Marshal(anchor)
	if err != nil {
		return nil, false
	}
	return data, true
}

// replaysFullHistory reports whether the wired memory provider replays the
// whole conversation on every turn (as opposed to serving a bounded window
// plus a folded summary).
func (s *Service) replaysFullHistory() bool {
	if s == nil || s.memory == nil {
		return false
	}
	rp, ok := s.memory.(interface{ ReplayFullHistory() bool })
	return ok && rp.ReplayFullHistory()
}

// usageAnchorCommitFactory builds the opencraft.usageanchor hook: at turn
// end it records the provider-measured prompt size so the next turn's fold
// decision starts from a measurement instead of a character estimate, and
// the same request's context snapshot, which is what the chat meter draws.
//
// The two records come from one board read and one hook because they
// describe one request: the anchor is the number the next fold is budgeted
// against, the snapshot is that same number split by what filled it. The
// impl name keeps saying "usageanchor" because it is a wire value — the
// deploy document names it, and a user layer may too.
type usageAnchorCommitFactory struct{}

var _ resource.Factory[agent.CommitterFunc] = usageAnchorCommitFactory{}

var depAnchorSessions = resource.Require[*ocsessions.Store]("sessions")

func (usageAnchorCommitFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "hook.commit",
		Impl: "opencraft.usageanchor",
		Deps: []resource.Requirement{depAnchorSessions.Spec()},
	}
}

func (usageAnchorCommitFactory) New(_ context.Context, in resource.Input) (agent.CommitterFunc, error) {
	store, err := depAnchorSessions.Get(in)
	if err != nil {
		return nil, err
	}
	return agent.CommitterFunc(func(
		ctx context.Context, _ agent.Identity, req *agent.Request, res *agent.Result,
	) error {
		var contextID string
		var board *agent.Board
		if req != nil {
			contextID = req.ContextID
		}
		if res != nil {
			board = res.LastBoard
		}
		recordUsageAnchor(ctx, store, contextID, board)
		recordContextUsage(ctx, store, contextID, board)
		return nil
	}), nil
}

// recordUsageAnchor persists one turn's measurement, if the turn produced
// one. Every guard here is a shape the harness cannot measure: a
// conversation with no session row, a turn that never reached the
// compaction node (a provider failure on the first call, an interrupt
// before it), or a provider that reported no usage.
func recordUsageAnchor(
	ctx context.Context,
	store *ocsessions.Store,
	contextID string,
	board *agent.Board,
) {
	if store == nil || board == nil || contextID == "" {
		return
	}
	// Delegated subagent runs are ephemeral ("ctx-..."), so there is no
	// conversation to anchor.
	if !ids.IsSession(contextID) {
		return
	}
	usage, ok := boardUsage(board)
	if !ok {
		return
	}
	anchored, ok := boardInt(board, config.BoardVarAnchorLen)
	if !ok || anchored <= 0 {
		return
	}
	// The measurement's generation is the conversation's fold count as of
	// the measurement. anchor_len's companion stamp is exact (it is written
	// next to the length); the cumulative total is the same number on the
	// turn's last round, which is the common case. Falling back to it keeps
	// the anchor usable when a fold happened after the last model call.
	epoch, ok := boardInt(board, config.BoardVarAnchorEpoch)
	if !ok {
		epoch, _ = boardInt(board, config.BoardVarEpochTotal)
	}
	anchor := ocsessions.UsageAnchor{
		// The inclusive prompt size, not the raw wire field: the anchor is
		// compared against a model's context window, which counts cached
		// and uncached prompt tokens alike (see sessions.PromptTokens).
		InputTokens:      ocsessions.PromptTokens(usage),
		AnchoredMessages: anchored,
		CompactCount:     epoch,
		Model:            usage.Model.ID.Name,
		At:               time.Now().UTC(),
	}
	if err := store.WriteUsageAnchor(contextID, anchor); err != nil {
		telemetry.WarnErr(ctx, "worldstate: persist usage anchor failed", err,
			attribute.String("conversation.id", contextID))
	}
}

// recordContextUsage persists the turn's context snapshot, if it produced
// one. The guards match recordUsageAnchor's — a delegated "ctx-" run has no
// conversation to meter, and a turn that never prepared or never called the
// model has nothing to describe — plus the one a snapshot adds: the earlier
// turn's numbers are more useful than a fresh empty one, so a turn that says
// nothing leaves the stored document alone.
func recordContextUsage(
	ctx context.Context,
	store *ocsessions.Store,
	contextID string,
	board *agent.Board,
) {
	if store == nil || board == nil || contextID == "" {
		return
	}
	if !ids.IsSession(contextID) {
		return
	}
	usage, ok := ContextUsageFromBoard(board)
	if !ok {
		return
	}
	usage.At = time.Now().UTC()
	if err := store.WriteContextUsage(contextID, usage); err != nil {
		telemetry.WarnErr(ctx, "worldstate: persist context usage failed", err,
			attribute.String("conversation.id", contextID))
	}
}

// CompactionReport is what automatic context compaction did during one
// turn, read from the board the turn ended with. It exists for the UI: a
// fold rewrites the conversation prefix, so the turn that folded is also
// the turn whose prompt cache went cold and whose model request was billed
// at full input price. The user cannot see that from the transcript.
type CompactionReport struct {
	// Folds is the number of successful folds in this turn.
	Folds int
	// Failures is the number of consecutive failed condensations standing
	// at the end of the turn (zero after a successful fold).
	Failures int
	// Notified reports whether the model was told that compaction is out
	// of options and the prompt is still over budget.
	Notified bool
}

// Empty reports whether compaction did nothing worth surfacing.
func (r CompactionReport) Empty() bool {
	return r.Folds == 0 && r.Failures == 0 && !r.Notified
}

// CompactionReportFromBoard reads the compaction bookkeeping off a finished
// turn's board. ok is false when the graph never reached the compaction
// node (a custom graph, a turn that failed before the first call).
func CompactionReportFromBoard(board *agent.Board) (CompactionReport, bool) {
	if board == nil {
		return CompactionReport{}, false
	}
	folds, ok := boardInt(board, config.BoardVarFoldsPerTurn)
	if !ok {
		return CompactionReport{}, false
	}
	failures, _ := boardInt(board, config.BoardVarFailStreak)
	noticeSent, _ := board.Var(config.BoardVarNoticeSent)
	notified, _ := noticeSent.(bool)
	return CompactionReport{
		Folds:    folds,
		Failures: failures,
		Notified: notified,
	}, true
}
