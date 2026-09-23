package compat

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/db"
)

// countingImporter is a WorkspaceImporter that only records how often
// the migration layer asks it for the search-index backfill.
type countingImporter struct {
	calls int
}

func (i *countingImporter) State(
	context.Context, string, string,
) ([]byte, bool, error) {
	return nil, false, nil
}

func (i *countingImporter) SetState(context.Context, string, string, []byte) error {
	return nil
}

func (i *countingImporter) ImportConversation(context.Context, WorkspaceImport) error {
	return nil
}

func (i *countingImporter) BackfillSearchIndex(context.Context) error {
	i.calls++
	return nil
}

var _ WorkspaceImporter = (*countingImporter)(nil)

// TestWorkspaceBackfillSearchIndexRunsOnce pins the migration contract:
// the index creation is a schema step that the open path (Workspace)
// performs, the backfill that fills the index for a database written
// before it exists is a separate step through the importer port —
// scheduled by the caller because it walks the whole archive — and runs
// exactly once, recorded as version 17.
func TestWorkspaceBackfillSearchIndexRunsOnce(t *testing.T) {
	ctx := context.Background()
	handle, err := db.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()

	importer := &countingImporter{}
	if err := Workspace(ctx, handle, t.TempDir(), importer); err != nil {
		t.Fatalf("workspace migration: %v", err)
	}
	// The open path is cheap: the index walk is not part of it.
	if importer.calls != 0 {
		t.Fatalf("backfill calls during Workspace = %d, want 0", importer.calls)
	}
	if err := BackfillSearchIndex(ctx, handle, importer); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if importer.calls != 1 {
		t.Fatalf("backfill calls = %d, want 1", importer.calls)
	}
	var name string
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT name FROM schema_migrations WHERE version = ?`,
		searchIndexVersion).Scan(&name); err != nil {
		t.Fatalf("search index step not recorded: %v", err)
	}
	if name != searchIndexName {
		t.Fatalf("recorded step = %q, want %q", name, searchIndexName)
	}
	var tables int
	if err := handle.SQLDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name = 'message_fts'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 1 {
		t.Fatal("message_fts table is missing after the workspace migration")
	}

	// Second open: the step is recorded, so the backfill does not run
	// again (the host schedules it on every open).
	if err := Workspace(ctx, handle, t.TempDir(), importer); err != nil {
		t.Fatalf("second workspace migration: %v", err)
	}
	if err := BackfillSearchIndex(ctx, handle, importer); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if importer.calls != 1 {
		t.Fatalf("backfill calls after reopen = %d, want 1", importer.calls)
	}
}
