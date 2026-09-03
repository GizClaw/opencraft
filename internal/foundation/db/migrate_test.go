package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateAppliesOnceInVersionOrder(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	migrations := []Migration{
		{Version: 2, Name: "second", Statements: []string{
			"CREATE TABLE second_tbl (id INTEGER PRIMARY KEY)",
		}},
		{Version: 1, Name: "first", Statements: []string{
			"CREATE TABLE first_tbl (id INTEGER PRIMARY KEY)",
			"INSERT INTO first_tbl(id) VALUES (1)",
		}},
	}
	if err := d.Migrate(context.Background(), migrations); err != nil {
		t.Fatal(err)
	}

	// Re-running is a no-op and does not fail on existing tables.
	if err := d.Migrate(context.Background(), migrations); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := d.SQLDB().QueryRow("SELECT COUNT(*) FROM first_tbl").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("first_tbl count = %d, want 1", count)
	}
	var versions int
	if err := d.SQLDB().QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("schema_migrations rows = %d, want 2", versions)
	}
}

func TestMigrateRejectsInvalidVersion(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Migrate(context.Background(), []Migration{
		{Version: 0, Name: "bad"},
	}); err == nil {
		t.Fatal("zero version migration accepted")
	}
}
