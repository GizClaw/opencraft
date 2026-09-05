package sessions

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
)

func importFixture() ImportRequest {
	return ImportRequest{
		Source: "codex:conv-1",
		Title:  "Imported title",
		Turns: []ImportTurn{
			{
				At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
				Messages: []message.Message{
					message.NewTextMessage(message.RoleUser, "hello"),
					message.NewTextMessage(message.RoleAssistant, "world"),
				},
			},
			{
				At: time.Date(2026, 1, 2, 3, 5, 0, 0, time.UTC),
				Messages: []message.Message{
					message.NewTextMessage(message.RoleUser, "again"),
				},
			},
		},
	}
}

func TestImportPersistsAndDedupes(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })
	ctx := context.Background()

	id, err := store.Import(ctx, importFixture())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !ValidID(id) {
		t.Fatalf("imported id %q is not an s- id", id)
	}
	ready, err := store.ImportReady(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("import reported ready before memory seed")
	}

	// Pending imports stay out of the resume list.
	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("pending import visible in List: %+v", metas)
	}

	history, err := store.History(ctx, id, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[0].Content.Text() != "hello" {
		t.Fatalf("history = %+v", history)
	}

	if err := store.CompleteImport(ctx, id); err != nil {
		t.Fatalf("CompleteImport: %v", err)
	}
	metas, err = store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].ID != id || metas[0].Title != "Imported title" {
		t.Fatalf("list after complete = %+v", metas)
	}

	// Idempotent import returns the same session.
	again, err := store.Import(ctx, importFixture())
	if err != nil {
		t.Fatalf("duplicate Import: %v", err)
	}
	if again != id {
		t.Fatalf("duplicate import = %q, want %q", again, id)
	}
}

func TestImportedBySources(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })
	ctx := context.Background()

	first := importFixture()
	id1, err := store.Import(ctx, first)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	second := importFixture()
	second.Source = "codex:conv-2"
	id2, err := store.Import(ctx, second)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	sources := []string{"codex:conv-1", "codex:conv-2", "codex:conv-1"}

	// Pending imports (memory seed not complete) are not reported as
	// importable duplicates.
	got, err := store.ImportedBySources(ctx, sources)
	if err != nil {
		t.Fatalf("ImportedBySources: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("pending imports reported: %+v", got)
	}

	if err := store.CompleteImport(ctx, id1); err != nil {
		t.Fatalf("CompleteImport: %v", err)
	}
	if err := store.CompleteImport(ctx, id2); err != nil {
		t.Fatalf("CompleteImport: %v", err)
	}
	got, err = store.ImportedBySources(ctx, sources)
	if err != nil {
		t.Fatalf("ImportedBySources: %v", err)
	}
	if got["codex:conv-1"] != id1 || got["codex:conv-2"] != id2 {
		t.Fatalf("imported sources = %+v, want %s->%s, %s->%s",
			got, "codex:conv-1", id1, "codex:conv-2", id2)
	}
	if _, ok := got["codex:missing"]; ok {
		t.Fatalf("missing source reported: %+v", got)
	}
}

func TestImportPendingReturnsSameIDWhileInFlight(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })

	id, err := store.Import(context.Background(), importFixture())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the memory seed still running by keeping the session
	// pending, then make sure the duplicate maps to the same id and
	// does not delete it.
	again, err := store.Import(context.Background(), importFixture())
	if err != nil {
		t.Fatalf("duplicate while pending: %v", err)
	}
	if again != id {
		t.Fatalf("duplicate while pending = %q, want %q", again, id)
	}
	if !store.Exists(id) {
		t.Fatal("pending session was deleted by duplicate import")
	}
}

func TestImportRequiresSourceAndArchiveableMessages(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })

	req := importFixture()
	req.Source = ""
	if _, err := store.Import(context.Background(), req); err == nil {
		t.Fatal("import without source accepted")
	}

	req = ImportRequest{
		Source: "codex:empty",
		Turns: []ImportTurn{{
			Messages: []message.Message{{Role: message.RoleUser}},
		}},
	}
	if _, err := store.Import(context.Background(), req); err == nil {
		t.Fatal("import with no archiveable content accepted")
	}
}

func TestImportPersistsTurnTimestamps(t *testing.T) {
	store, err := newMigratedStore(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseDB() })
	ctx := context.Background()

	req := importFixture()
	requested := time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC)
	started := time.Date(2026, 1, 2, 3, 4, 7, 0, time.UTC)
	finished := time.Date(2026, 1, 2, 3, 4, 30, 0, time.UTC)
	req.Turns[0].RequestedAt = &requested
	req.Turns[0].StartedAt = &started
	req.Turns[0].FinishedAt = &finished

	id, err := store.Import(ctx, req)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	turns, err := store.Turns(ctx, id)
	if err != nil {
		t.Fatalf("Turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(turns))
	}
	first := turns[0]
	if !first.At.Equal(req.Turns[0].At) ||
		!first.RequestedAt.Equal(requested) ||
		!first.StartedAt.Equal(started) ||
		!first.FinishedAt.Equal(finished) {
		t.Fatalf("turn 1 timing = at %v requested %v started %v finished %v",
			first.At, first.RequestedAt, first.StartedAt, first.FinishedAt)
	}
	second := turns[1]
	if !second.FinishedAt.Equal(second.At) ||
		!second.FinishedAt.Equal(req.Turns[1].At) {
		t.Fatalf("turn 2 without finished_at = %+v", second)
	}
}
