package sessions

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
)

// A run archives once, and a repeated archive is a no-op that consumes
// nothing. The repeat is rejected before the per-run buffers are read:
// a late artifact buffered under the same run after its turn landed is
// not eaten by a commit that then writes nothing.
func TestRepeatedArchiveOfOneRunIsANoOp(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	msgs := []message.Message{
		message.NewTextMessage(message.RoleUser, "写一个文件"),
		message.NewTextMessage(message.RoleAssistant, "写好了"),
	}
	at := time.Date(2026, 9, 3, 1, 2, 3, 0, time.UTC)
	if err := store.RecordTurnTiming(id, "run-1", at, at); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnWithRunID(ctx, id, "run-1", msgs); err != nil {
		t.Fatal(err)
	}

	// A writer buffered one more file under the run after the turn was
	// archived, and then the same run's archive arrives again.
	if err := store.BufferArtifact(id, "run-1", "out.txt", 3); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnWithRunID(ctx, id, "run-1", msgs); err != nil {
		t.Fatalf("repeated archive: %v", err)
	}

	turns, err := store.Turns(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want the run archived once", len(turns))
	}
	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.ID != id {
			continue
		}
		if m.Turns != 1 || m.Messages != 2 {
			t.Fatalf("counts = %d turns / %d messages, want 1 / 2",
				m.Turns, m.Messages)
		}
	}
	if got := store.takeArtifacts(id, "run-1"); len(got) != 1 ||
		got[0].Path != "out.txt" {
		t.Fatalf("buffered artifacts = %+v, want the late write left in place", got)
	}
}
