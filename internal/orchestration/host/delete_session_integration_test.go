package host_test

import (
	"context"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/ids"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestHostDeleteConversationCancelsLiveRun verifies the desktop
// deletion path: deleting a conversation with a live run cancels the
// run immediately, waits for its terminal persistence, removes the
// stored conversation, and tombstones the id so a stale StartRun can
// never mint the same session again.
func TestHostDeleteConversationCancelsLiveRun(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	gate := provider.HoldNext()
	defer gate.Release()
	h, _ := acquireHostFixture(t, provider)
	ctx := context.Background()

	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "blocked run"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	select {
	case <-gate.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("provider request did not start")
	}

	// Production callers always wait on the run; deletion must not
	// depend on the order of this goroutine relative to Delete.
	waitDone := make(chan error, 1)
	go func() {
		_, waitErr := run.Wait(context.Background())
		waitDone <- waitErr
	}()

	delDone := make(chan error, 1)
	go func() {
		delDone <- h.DeleteConversation(context.Background(), run.ContextID())
	}()
	select {
	case err := <-delDone:
		if err != nil {
			t.Fatalf("DeleteConversation while run active: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("DeleteConversation did not stop the live run in time")
	}

	if h.Sessions().Exists(run.ContextID()) {
		t.Fatal("conversation still exists after delete")
	}
	select {
	case <-waitDone:
	case <-time.After(30 * time.Second):
		t.Fatal("run wait did not settle after deletion")
	}

	// A stale explicit start on the deleted id must be refused, not
	// mint a fresh conversation under the same id.
	if _, err := h.StartRun(ctx, host.RunOptions{
		Message:   message.NewTextMessage(message.RoleUser, "again"),
		ContextID: run.ContextID(),
	}); err == nil {
		t.Fatal("StartRun on a deleted session succeeded, want refusal")
	}

	// Deletion is idempotent once complete.
	if err := h.DeleteConversation(context.Background(), run.ContextID()); err != nil {
		t.Fatalf("second DeleteConversation: %v", err)
	}
}

// TestHostDeleteConversationRemovesRowsThatCameBack pins the repeat
// delete path. The tombstone stops the id from being minted again, but
// the rows are what the sidebar lists: an entry that reappears under a
// tombstoned id (a late write from an older build, a store restored out
// of band) must still be deletable — otherwise it stays on screen for
// the rest of the Host's life as an entry that can neither be opened nor
// deleted again, because the frontend has the id tombstoned too. The
// repeat call also stays a success, and its purge takes the checkpoint a
// resurrected id could otherwise be recovered from.
func TestHostDeleteConversationRemovesRowsThatCameBack(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	h, _ := acquireHostFixture(t, provider)
	ctx := context.Background()

	id, err := h.Sessions().Create()
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := h.DeleteConversation(ctx, id); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	// The row is back, the way a late usage write from an older build
	// (or a store restored from a backup) put it there: a build that
	// does not know about deleted_conversations upserts the row
	// directly, carrying the usage totals the write was about. The
	// fixture writes it the same way, behind every guarded writer the
	// store owns, because that is the only way the row can come back.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := h.Sessions().State().Handle().SQLDB().ExecContext(ctx, `
		INSERT INTO conversations(
			id, title, created_at, updated_at, turn_count, message_count,
			usage_json, import_source, import_ready
		) VALUES (?, '', ?, ?, 0, 0, ?, '', 0)
		ON CONFLICT(id) DO UPDATE SET usage_json = excluded.usage_json`,
		id, now, now, `{"total_tokens":42}`); err != nil {
		t.Fatalf("recreate conversation row: %v", err)
	}
	// A crash-recovery checkpoint for the id, which the purge has to
	// take with the rows.
	if err := h.Sessions().Save(ctx, agent.Checkpoint{
		ExecID:    ids.RunPrefix + "came-back",
		Steps:     []string{"world"},
		Iteration: 1,
		Board:     &agent.BoardSnapshot{Vars: map[string]any{}},
		Attributes: map[string]string{
			"oc.conversation_id": id,
		},
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed run checkpoint: %v", err)
	}

	// The resurrected entry is what the sidebar reads, so the fixture
	// asserts the symptom the repeat delete exists for.
	listed := func() bool {
		t.Helper()
		metas, err := h.Sessions().List()
		if err != nil {
			t.Fatalf("list conversations: %v", err)
		}
		for _, meta := range metas {
			if meta.ID == id {
				return true
			}
		}
		return false
	}
	if !listed() {
		t.Fatal("setup: the resurrected conversation is not listed")
	}

	if err := h.DeleteConversation(ctx, id); err != nil {
		t.Fatalf("second DeleteConversation: %v", err)
	}
	if h.Sessions().Exists(id) {
		t.Fatal("conversation still exists after the repeat delete")
	}
	if listed() {
		t.Fatal("conversation still listed after the repeat delete")
	}
	if runs := runCheckpointIDs(t, h); len(runs) != 0 {
		t.Fatalf("run checkpoints after the repeat delete = %v, want none", runs)
	}
	// The tombstone outlives both deletes and the purge: a stale start
	// is refused for the same reason it was refused the first time.
	if _, err := h.StartRun(ctx, host.RunOptions{
		Message:   message.NewTextMessage(message.RoleUser, "again"),
		ContextID: id,
	}); err == nil {
		t.Fatal("StartRun on a repeatedly deleted session succeeded, want refusal")
	}
}

// TestHostDeleteConversationRemovesIdleConversation pins the no-run
// path: an idle conversation's settings rows disappear with the
// delete, and deleting an unknown id stays a no-op.
func TestHostDeleteConversationRemovesIdleConversation(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	h, _ := acquireHostFixture(t, provider)
	ctx := context.Background()

	id := ids.NewSession()
	if err := h.Sessions().SetModel(ctx, id, "fake-model"); err != nil {
		t.Fatalf("seed session settings: %v", err)
	}
	if err := h.DeleteConversation(ctx, id); err != nil {
		t.Fatalf("DeleteConversation on idle session: %v", err)
	}
	if h.Sessions().Exists(id) {
		t.Fatal("conversation still exists after delete")
	}
	if model, err := h.Sessions().Model(ctx, id); err != nil || model != "" {
		t.Fatalf("Model after delete = %q, %v; want \"\", nil", model, err)
	}
}
