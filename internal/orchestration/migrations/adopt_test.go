package migrations

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAdoptLegacySessionsMovesTree(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	workDir := filepath.Join(base, "work")
	legacyRoot := LegacySessionsDir(workDir)
	historyDir := filepath.Join(legacyRoot, "s-1", "history")
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(historyDir, "000001.json"),
		[]byte("{}"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	destRoot := filepath.Join(base, "workspaces", "wid", "sessions")

	if err := AdoptLegacySessions(ctx, legacyRoot, destRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(
		destRoot, "s-1", "history", "000001.json",
	)); err != nil {
		t.Fatalf("legacy file not adopted: %v", err)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy root still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".opencraft")); !os.IsNotExist(err) {
		t.Fatalf("empty .opencraft parent was not cleaned: %v", err)
	}
}

func TestAdoptLegacySessionsSkipsWhenNewDBExists(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	workDir := filepath.Join(base, "work")
	legacyRoot := LegacySessionsDir(workDir)
	if err := os.MkdirAll(filepath.Join(legacyRoot, "s-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	destRoot := filepath.Join(base, "workspaces", "wid", "sessions")
	if err := os.MkdirAll(destRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(destRoot, "session.db"), []byte("new"), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := AdoptLegacySessions(ctx, legacyRoot, destRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacyRoot, "s-1")); err != nil {
		t.Fatalf("legacy root should stay untouched when session.db exists: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(destRoot, "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("session.db = %q, want untouched", data)
	}
}

func TestAdoptLegacySessionsSkipsPopulatedDestination(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	workDir := filepath.Join(base, "work")
	legacyRoot := LegacySessionsDir(workDir)
	if err := os.MkdirAll(filepath.Join(legacyRoot, "s-legacy"), 0o700); err != nil {
		t.Fatal(err)
	}
	destRoot := filepath.Join(base, "workspaces", "wid", "sessions")
	if err := os.MkdirAll(filepath.Join(destRoot, "s-existing"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := AdoptLegacySessions(ctx, legacyRoot, destRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacyRoot, "s-legacy")); err != nil {
		t.Fatalf("legacy root should stay when destination is populated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destRoot, "s-existing")); err != nil {
		t.Fatalf("populated destination changed: %v", err)
	}
}

func TestAdoptLegacySessionsMissingSourceIsNoop(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	destRoot := filepath.Join(base, "workspaces", "wid", "sessions")
	if err := AdoptLegacySessions(
		ctx, LegacySessionsDir(filepath.Join(base, "work")), destRoot,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destRoot); !os.IsNotExist(err) {
		t.Fatalf("destination created for missing legacy root: %v", err)
	}
}

func TestCopyLegacyTreeCopiesNestedFiles(t *testing.T) {
	src := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(filepath.Join(src, "s-1", "history"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(src, "s-1", "history", "000001.json"),
		[]byte("legacy"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "adopted")

	if err := copyLegacyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(
		dst, "s-1", "history", "000001.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "legacy" {
		t.Fatalf("copied file = %q, want legacy", data)
	}
	if _, err := os.Stat(filepath.Join(src, "s-1", "history")); err != nil {
		t.Fatalf("source should be untouched by copy: %v", err)
	}
}

func TestAdoptLegacySessionsSkipsSymlinkedParent(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "sessions", "s-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(base, "repo")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(workDir, ".opencraft")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	destRoot := filepath.Join(base, "workspaces", "wid", "sessions")

	if err := AdoptLegacySessions(
		ctx, filepath.Join(workDir, ".opencraft", "sessions"), destRoot,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".opencraft", "sessions")); err != nil {
		t.Fatalf("symlinked legacy root must not be moved: %v", err)
	}
	if _, err := os.Stat(destRoot); !os.IsNotExist(err) {
		t.Fatalf("destination created for symlinked legacy root: %v", err)
	}
}
