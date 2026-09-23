package host_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// crashCheckpointBoard builds the board a run killed after its first
// wave leaves behind: world-state sections, then the turn's messages.
func crashCheckpointBoard(conversationID string) *agent.Board {
	board := agent.NewBoard()
	board.SetVar("world.sections.count", 1)
	board.SetVar("oc_thread_id", "oc-"+conversationID)
	board.AppendChannelMessage(agent.MainChannel, message.NewTextMessage(
		message.RoleSystem, "world context section"))
	board.AppendChannelMessage(agent.MainChannel, message.NewTextMessage(
		message.RoleUser, "hello from the board (inlined form)"))
	board.AppendChannelMessage(agent.MainChannel, message.NewTextMessage(
		message.RoleAssistant, "partial answer before the crash"))
	board.AppendChannelMessage(agent.MainChannel, message.NewTextMessage(
		message.RoleTool, "partial tool output"))
	return board
}

func crashCheckpoint(conversationID, runID string) agent.Checkpoint {
	request := agent.Request{
		ContextID: conversationID,
		Message: message.NewTextMessage(
			message.RoleUser, "hello from the crashed run"),
	}
	raw, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	return agent.Checkpoint{
		ExecID:    runID,
		Steps:     []string{"world"},
		Iteration: 1,
		Board:     crashCheckpointBoard(conversationID).Snapshot(),
		// Older than this process: the checkpoint was written by the
		// process that crashed, which is exactly what recovery keys on.
		Timestamp:         time.Now().Add(-time.Minute),
		OriginalStartedAt: time.Now().Add(-2 * time.Minute),
		Attributes: map[string]string{
			"oc.conversation_id": conversationID,
			"oc.request":         string(raw),
		},
	}
}

func countArchiveMessages(t *testing.T, h *host.Host, conversationID string) int {
	t.Helper()
	var n int
	if err := h.Sessions().Database().SQLDB().QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM archive_messages WHERE conversation_id = ?`,
		conversationID,
	).Scan(&n); err != nil {
		t.Fatalf("count archive messages: %v", err)
	}
	return n
}

func acquireHost(
	t *testing.T, dataDir, configDir, workDir string,
) (*host.Manager, *host.Host) {
	t.Helper()
	mgr := host.NewManagerAt(dataDir, configDir)
	h, err := mgr.Acquire(context.Background(), workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return mgr, h
}

// TestHostRecoverySkipsWhatItCannotRecover pins the guard rails around
// the crash pass: delegated contexts and deleted conversations are
// dropped, a checkpoint a sibling process may still be writing is left
// alone, and a checkpoint without the request annotation still recovers
// from the board form.
func TestHostRecoverySkipsWhatItCannotRecover(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "first reply"})
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	ctx := context.Background()
	_, h1 := acquireHost(t, dataDir, configDir, workDir)
	run, err := h1.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hello"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("wait run: %v", err)
	}
	conversationID := run.ContextID()
	stateStore := h1.Sessions().State()

	delegated := crashCheckpoint(conversationID, "run-delegated0001")
	delegated.Attributes["oc.conversation_id"] = "ctx-delegated0001"
	gone := crashCheckpoint(conversationID, "run-gone0001")
	gone.Attributes["oc.conversation_id"] = "s-gone0001"
	fresh := crashCheckpoint(conversationID, "run-fresh0001")
	fresh.Timestamp = time.Now().Add(time.Minute)
	boardOnly := crashCheckpoint(conversationID, "run-boardonly01")
	delete(boardOnly.Attributes, "oc.request")
	for _, cp := range []agent.Checkpoint{delegated, gone, fresh, boardOnly} {
		if err := stateStore.Save(ctx, cp); err != nil {
			t.Fatalf("save checkpoint %s: %v", cp.ExecID, err)
		}
	}
	if err := h1.Close(); err != nil {
		t.Fatalf("close first host: %v", err)
	}

	_, h2 := acquireHost(t, dataDir, configDir, workDir)
	report, ok := h2.RecoveryReport()
	if !ok {
		t.Fatal("recovery pass did not run")
	}
	want := host.RecoveryReport{
		At:          report.At,
		Recovered:   1,
		Discarded:   2,
		SkippedLive: 1,
	}
	if report != want {
		t.Fatalf("recovery report = %+v, want %+v", report, want)
	}
	turns, err := h2.Sessions().Turns(ctx, conversationID)
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	recovered := turns[len(turns)-1]
	if recovered.RunID != "run-boardonly01" {
		t.Fatalf("recovered run = %q, want the board-only checkpoint",
			recovered.RunID)
	}
	if got := recovered.Messages[0].Content.Text(); got != "hello from the board (inlined form)" {
		t.Fatalf("board-form user message = %q", got)
	}
	// The checkpoint a sibling process may own stays for a later pass.
	if ids := runCheckpointIDs(t, h2); len(ids) != 1 || ids[0] != "run-fresh0001" {
		t.Fatalf("remaining checkpoints = %v, want the fresh one only", ids)
	}
}

// TestHostRecoversCrashedTurn pins the L1-4 delivery: a run killed
// mid-turn leaves a checkpoint with no archive row, and the next
// assembly materializes it as an interrupted turn — user message in its
// original form, partial assistant/tool output intact, memory written
// in the same transaction — and a second recovery of the same run
// changes nothing.
func TestHostRecoversCrashedTurn(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "first reply"})
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	ctx := context.Background()
	_, h1 := acquireHost(t, dataDir, configDir, workDir)
	run, err := h1.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hello"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("wait run: %v", err)
	}
	conversationID := run.ContextID()
	store := h1.Sessions()
	baselineTurns, err := store.Turns(ctx, conversationID)
	if err != nil {
		t.Fatalf("turns before recovery: %v", err)
	}
	baselineMessages := countArchiveMessages(t, h1, conversationID)

	// The crash: a run checkpoint survives with no archive row.
	const runID = "run-crashed0001"
	cp := crashCheckpoint(conversationID, runID)
	if err := store.State().Save(ctx, cp); err != nil {
		t.Fatalf("save crash checkpoint: %v", err)
	}
	if err := h1.Close(); err != nil {
		t.Fatalf("close first host: %v", err)
	}

	// Restart: a fresh manager opens the same store and recovers.
	_, h2 := acquireHost(t, dataDir, configDir, workDir)
	report, ok := h2.RecoveryReport()
	if !ok {
		t.Fatal("recovery pass did not run")
	}
	if report.Recovered != 1 || report.Failed != 0 {
		t.Fatalf("recovery report = %+v, want one recovered turn", report)
	}
	turns, err := h2.Sessions().Turns(ctx, conversationID)
	if err != nil {
		t.Fatalf("turns after recovery: %v", err)
	}
	if len(turns) != len(baselineTurns)+1 {
		t.Fatalf("turns after recovery = %d, want %d",
			len(turns), len(baselineTurns)+1)
	}
	recovered := turns[len(turns)-1]
	if recovered.RunID != runID ||
		recovered.Status != "interrupted" ||
		recovered.InterruptCause != "app_restart" {
		t.Fatalf("recovered turn = %+v, want interrupted run %s from app_restart",
			recovered, runID)
	}
	var texts []string
	for _, m := range recovered.Messages {
		texts = append(texts, m.Content.Text())
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{
		"hello from the crashed run",
		"partial answer before the crash",
		"partial tool output",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("recovered turn messages missing %q:\n%s", want, joined)
		}
	}
	// The board's media-inlined form must not be what got archived.
	if strings.Contains(joined, "inlined form") {
		t.Fatalf("recovered turn kept the inline board form:\n%s", joined)
	}
	if strings.Contains(joined, "world context section") {
		t.Fatalf("recovered turn archived world-state context:\n%s", joined)
	}
	if after := countArchiveMessages(t, h2, conversationID); after <= baselineMessages {
		t.Fatalf("transcript messages = %d, want more than %d after recovery",
			after, baselineMessages)
	}
	if ids := runCheckpointIDs(t, h2); len(ids) != 0 {
		t.Fatalf("checkpoints after recovery = %v, want none", ids)
	}

	// A crash between the write and the checkpoint delete (or any
	// repeated pass) must not duplicate the turn or its messages.
	messagesAfterRecovery := countArchiveMessages(t, h2, conversationID)
	if err := h2.Sessions().State().Save(ctx, cp); err != nil {
		t.Fatalf("re-save crash checkpoint: %v", err)
	}
	if err := h2.Close(); err != nil {
		t.Fatalf("close second host: %v", err)
	}
	_, h3 := acquireHost(t, dataDir, configDir, workDir)
	report3, _ := h3.RecoveryReport()
	if report3.Recovered != 0 || report3.Archived != 1 {
		t.Fatalf("second pass report = %+v, want one archived leftover", report3)
	}
	turns3, err := h3.Sessions().Turns(ctx, conversationID)
	if err != nil {
		t.Fatalf("turns after second pass: %v", err)
	}
	if len(turns3) != len(turns) {
		t.Fatalf("turns after second pass = %d, want %d", len(turns3), len(turns))
	}
	if after := countArchiveMessages(t, h3, conversationID); after != messagesAfterRecovery {
		t.Fatalf("transcript messages after second pass = %d, want %d",
			after, messagesAfterRecovery)
	}
	if ids := runCheckpointIDs(t, h3); len(ids) != 0 {
		t.Fatalf("checkpoints after second pass = %v, want none", ids)
	}
}
