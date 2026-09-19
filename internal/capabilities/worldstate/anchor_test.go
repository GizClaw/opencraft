package worldstate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// replayAnchorService is a worldstate service wired the way the cross-turn
// anchor expects: full-history replay plus a session store. Windowed
// deployments never see the anchor (see usageAnchorBoardValue), so the tests
// of the anchor's own guards start from this shape.
func replayAnchorService(store *ocsessions.Store) *Service {
	svc := &Service{sessionStore: store}
	svc.SetMemory(replayMemory{})
	return svc
}

// TestUsageAnchorRoundTrip pins the two halves of the anchor contract: the
// turn-end hook records the provider's measurement of one request, and the
// next turn's prepare hook hands it to the graph's compaction node.
func TestUsageAnchorRoundTrip(t *testing.T) {
	store := newSessionStore(t)
	const contextID = "s-anchor-1"

	board := agent.NewBoard()
	board.SetVar(config.BoardVarLLMUsage, inference.Usage{
		InputTokens: 12000,
		Model: model.ModelRef{ID: model.ModelID{
			Provider: "openai", Name: "gpt-6-astra",
		}},
	})
	board.SetVar(config.BoardVarAnchorLen, int64(42))
	board.SetVar(config.BoardVarAnchorEpoch, int64(2))
	recordUsageAnchor(context.Background(), store, contextID, board)

	svc := replayAnchorService(store)
	data, ok := svc.usageAnchorBoardValue(context.Background(), contextID)
	if !ok {
		t.Fatal("no anchor exposed to the graph after a measured turn")
	}
	var got ocsessions.UsageAnchor
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("anchor payload is not the shape the node parses: %v", err)
	}
	if got.InputTokens != 12000 || got.AnchoredMessages != 42 ||
		got.CompactCount != 2 || got.Model != "gpt-6-astra" {
		t.Fatalf("anchor = %+v, want the recorded measurement", got)
	}
}

// TestUsageAnchorSkipsUnmeasurableTurns pins the guards: a turn whose
// provider reported nothing, or that never reached the compaction node,
// must not shadow a usable earlier measurement with zeros.
func TestUsageAnchorSkipsUnmeasurableTurns(t *testing.T) {
	store := newSessionStore(t)
	const contextID = "s-anchor-2"
	svc := replayAnchorService(store)

	ctx := context.Background()
	if _, ok := svc.usageAnchorBoardValue(ctx, contextID); ok {
		t.Fatal("a conversation with no measured turn must expose no anchor")
	}

	// No usage: the node ran, but the provider reported no tokens.
	noUsage := agent.NewBoard()
	noUsage.SetVar(config.BoardVarAnchorLen, int64(10))
	recordUsageAnchor(ctx, store, contextID, noUsage)
	if _, ok := svc.usageAnchorBoardValue(ctx, contextID); ok {
		t.Fatal("a turn without provider usage must not write an anchor")
	}

	// Usage but no channel length: the turn failed before the node sized
	// the prompt, so the count does not describe the measurement.
	noLength := agent.NewBoard()
	noLength.SetVar(config.BoardVarLLMUsage, inference.Usage{InputTokens: 900})
	recordUsageAnchor(ctx, store, contextID, noLength)
	if _, ok := svc.usageAnchorBoardValue(ctx, contextID); ok {
		t.Fatal("a turn without a channel length must not write an anchor")
	}

	// Ephemeral subagent conversations have no session row to write to.
	sub := agent.NewBoard()
	sub.SetVar(config.BoardVarLLMUsage, inference.Usage{InputTokens: 900})
	sub.SetVar(config.BoardVarAnchorLen, int64(10))
	recordUsageAnchor(ctx, store, "ctx-0f1e2d3c4b5a6978", sub)
	if _, ok := svc.usageAnchorBoardValue(ctx, "ctx-0f1e2d3c4b5a6978"); ok {
		t.Fatal("a subagent conversation must not be anchored")
	}
}

// TestUsageAnchorSurvivesReadFailure pins the read side's contract: a
// conversation whose anchor document is missing or corrupt reports
// "no anchor" instead of failing the turn.
func TestUsageAnchorSurvivesReadFailure(t *testing.T) {
	store := newSessionStore(t)
	const contextID = "s-anchor-3"
	svc := replayAnchorService(store)
	ctx := context.Background()

	if _, ok := svc.usageAnchorBoardValue(ctx, contextID); ok {
		t.Fatal("missing anchor must read as no anchor")
	}
	if err := store.WriteState(contextID, "usage_anchor", "not an object"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.usageAnchorBoardValue(ctx, contextID); ok {
		t.Fatal("an unreadable anchor must read as no anchor")
	}
	if _, err := store.ReadUsageAnchor(contextID); err == nil {
		t.Fatal("the store must surface the decode failure to callers")
	}
}

// TestUsageAnchorRidesBoardNotSections pins where the anchor travels: as a
// board var for the harness, never as model-visible world-state content.
func TestUsageAnchorRidesBoardNotSections(t *testing.T) {
	store := newSessionStore(t)
	const contextID = "s-anchor-4"
	board := agent.NewBoard()
	board.SetVar(config.BoardVarLLMUsage, inference.Usage{InputTokens: 5000})
	board.SetVar(config.BoardVarAnchorLen, int64(7))
	recordUsageAnchor(context.Background(), store, contextID, board)

	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetSessions(store)
	// Only a full-replay deployment gets the cross-turn anchor: see
	// usageAnchorBoardValue for why a sliding window cannot use it.
	svc.SetMemory(replayMemory{})
	rendered := boardOf(t, svc, contextID, "hi")
	data, ok := svc.usageAnchorBoardValue(context.Background(), contextID)
	if !ok {
		t.Fatal("prepare must expose the recorded anchor")
	}
	if rendered.GetVarString(config.BoardVarUsageAnchor) != string(data) {
		t.Fatalf("board anchor = %q, want %q",
			rendered.GetVarString(config.BoardVarUsageAnchor), string(data))
	}
	sections := unmarshalSections(t, rendered)
	for _, sec := range sections {
		if sec.Content.Text() != "" &&
			contains(sec.Content.Text(), "anchored_messages") {
			t.Fatalf("anchor leaked into model-visible section %q", sec.ID)
		}
	}
}

// TestUsageAnchorWithheldFromWindowedMemory pins the gate itself. A bounded
// memory window slides between turns, so the previous measurement describes
// a prompt this turn does not send; the node's coverage check cannot tell
// (it compares a message count against a channel length), and the anchor
// would then set a floor above the real prompt — folding conversations that
// fit. The record is still written, so the measurement is available the
// moment the deployment switches to replay.
func TestUsageAnchorWithheldFromWindowedMemory(t *testing.T) {
	store := newSessionStore(t)
	const contextID = "s-anchor-6"
	board := agent.NewBoard()
	board.SetVar(config.BoardVarLLMUsage, inference.Usage{InputTokens: 5000})
	board.SetVar(config.BoardVarAnchorLen, int64(9))
	recordUsageAnchor(context.Background(), store, contextID, board)

	svc := &Service{sessionStore: store}
	svc.SetMemory(stubMemory{}) // windowed: a summary plus a raw window
	ctx := context.Background()
	if _, ok := svc.usageAnchorBoardValue(ctx, contextID); ok {
		t.Fatal("windowed memory must not receive a cross-turn anchor")
	}
	if got := boardOf(t, svc, contextID, "hi").GetVarString(config.BoardVarUsageAnchor); got != "" {
		t.Fatalf("board anchor = %q, want it unset for a windowed deployment", got)
	}
	// The measurement itself survives for the switch to full replay.
	if _, err := store.ReadUsageAnchor(contextID); err != nil {
		t.Fatalf("the anchor record must still be written: %v", err)
	}
}

// TestCompactionReportFromBoard pins the read side of the compaction
// bookkeeping the UI renders: a turn that folded, a turn whose folds keep
// failing, and a turn that never reached the node.
func TestCompactionReportFromBoard(t *testing.T) {
	if _, ok := CompactionReportFromBoard(agent.NewBoard()); ok {
		t.Fatal("a board the node never ran on must report no compaction")
	}
	if _, ok := CompactionReportFromBoard(nil); ok {
		t.Fatal("a nil board must report no compaction")
	}

	quiet := agent.NewBoard()
	quiet.SetVar(config.BoardVarFoldsPerTurn, int64(0))
	quiet.SetVar(config.BoardVarFailStreak, int64(0))
	report, ok := CompactionReportFromBoard(quiet)
	if !ok || !report.Empty() {
		t.Fatalf("report = %+v (ok=%v), want an empty report", report, ok)
	}

	folded := agent.NewBoard()
	folded.SetVar(config.BoardVarFoldsPerTurn, int64(2))
	folded.SetVar(config.BoardVarFailStreak, float64(0))
	report, ok = CompactionReportFromBoard(folded)
	if !ok || report.Folds != 2 || report.Failures != 0 || report.Notified {
		t.Fatalf("report = %+v, want two folds and nothing else", report)
	}
	if report.Empty() {
		t.Fatal("a folded turn is not an empty report")
	}

	strained := agent.NewBoard()
	strained.SetVar(config.BoardVarFoldsPerTurn, int64(0))
	strained.SetVar(config.BoardVarFailStreak, int64(3))
	strained.SetVar(config.BoardVarNoticeSent, true)
	report, ok = CompactionReportFromBoard(strained)
	if !ok || report.Folds != 0 || report.Failures != 3 || !report.Notified {
		t.Fatalf("report = %+v, want the failure streak and the notice", report)
	}
}

// TestUsageAnchorKeepsTheFoldGeneration pins the diagnostic the anchor
// stores per measurement: the conversation's fold generation, so a stored
// record can be told apart from one taken after the prefix was rewritten.
func TestUsageAnchorKeepsTheFoldGeneration(t *testing.T) {
	store := newSessionStore(t)
	const contextID = "s-anchor-5"
	board := agent.NewBoard()
	board.SetVar(config.BoardVarLLMUsage, inference.Usage{InputTokens: 4000})
	board.SetVar(config.BoardVarAnchorLen, int64(12))
	board.SetVar(config.BoardVarEpochTotal, int64(4))
	recordUsageAnchor(context.Background(), store, contextID, board)

	anchor, err := store.ReadUsageAnchor(contextID)
	if err != nil {
		t.Fatal(err)
	}
	if anchor.CompactCount != 4 {
		t.Fatalf("anchor generation = %d, want the cumulative 4", anchor.CompactCount)
	}
}
