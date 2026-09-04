package migrations

import (
	"testing"
	"testing/fstest"
)

func TestDiscoverMigrationsPreservesNumericVersions(t *testing.T) {
	sqlFS := fstest.MapFS{
		"sql/workspace/001_baseline.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE baseline (id TEXT);"),
		},
		"sql/workspace/002_add_column.sql": &fstest.MapFile{
			Data: []byte("ALTER TABLE baseline ADD COLUMN extra TEXT;"),
		},
	}

	migrations := discoverMigrations(sqlFS, "sql/workspace")
	if len(migrations) != 2 {
		t.Fatalf("discovered %d migrations, want 2", len(migrations))
	}
	if migrations[0].Version != 1 ||
		migrations[0].Name != "001_baseline" {
		t.Fatalf("first migration = %+v", migrations[0])
	}
	if migrations[1].Version != 2 ||
		migrations[1].Name != "002_add_column" {
		t.Fatalf("second migration = %+v", migrations[1])
	}
}
