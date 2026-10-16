package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	corews "github.com/GizClaw/flowcraft/core/workspace"
)

// TestReadonlyRootSymlinkDoesNotEscape pins the readonly-root boundary:
// readonly roots are writable from the exec sandbox (the cache dir by
// design), so a symlink planted inside one must not let a workspace
// read, bounded read, list, stat or exists call reach a target outside
// it. Reads that stay inside the root keep working.
func TestReadonlyRootSymlinkDoesNotEscape(t *testing.T) {
	base := t.TempDir()
	workRoot := filepath.Join(base, "work")
	cacheDir := filepath.Join(base, "cache")
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// The sandboxed command plants the links: the cache root is
	// writable from inside the box.
	secret := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideDir := filepath.Join(base, "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "f.txt"), []byte("X"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "plain.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(cacheDir, "leak")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(cacheDir, "dirlink")); err != nil {
		t.Fatal(err)
	}
	// A link that stays inside the root keeps being served: os.Root
	// resolves relative links, it only refuses escapes.
	if err := os.Symlink("plain.txt", filepath.Join(cacheDir, "inside")); err != nil {
		t.Fatal(err)
	}

	confined, err := corews.NewLocalWorkspace(workRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cerr := confined.Close(); cerr != nil {
			t.Logf("close confined: %v", cerr)
		}
	}()

	// Paths are spelled through the resolved root: the readonly list
	// stores resolved roots (dedupePaths), so requests must use the
	// same form for the lexical match to select the root.
	realCache, err := filepath.EvalSymlinks(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	cw := &confinedWithReadonly{
		confined: confined,
		root:     confined.Root(),
		readonly: []string{realCache},
	}
	ctx := context.Background()
	join := func(rel string) string { return filepath.Join(realCache, rel) }

	// Control: regular files inside the root still read.
	data, err := cw.Read(ctx, join("plain.txt"))
	if err != nil || string(data) != "ok" {
		t.Fatalf("readonly read = %q, %v; want ok", data, err)
	}
	data, err = cw.ReadLimited(ctx, join("plain.txt"), 1<<20)
	if err != nil || string(data) != "ok" {
		t.Fatalf("readonly bounded read = %q, %v; want ok", data, err)
	}
	if data, err := cw.Read(ctx, join("inside")); err != nil || string(data) != "ok" {
		t.Fatalf("readonly read through an inside link = %q, %v; want ok", data, err)
	}

	// Symlinks leaving the root are denied on every read path.
	if data, err := cw.Read(ctx, join("leak")); err == nil {
		t.Fatalf("readonly read followed a symlink out of the root: %q", data)
	}
	if _, err := cw.ReadLimited(ctx, join("leak"), 1<<20); err == nil {
		t.Fatal("readonly bounded read followed a symlink out of the root")
	}
	if entries, err := cw.List(ctx, join("dirlink")); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("readonly list followed a directory symlink out of the root: %v", names)
	}
	if info, err := cw.Stat(ctx, join("leak")); err == nil {
		t.Fatalf("readonly stat followed a symlink out of the root: %v", info.Name())
	}
	if ok, err := cw.Exists(ctx, join("leak")); ok && err == nil {
		t.Fatal("readonly exists followed a symlink out of the root")
	}
}
