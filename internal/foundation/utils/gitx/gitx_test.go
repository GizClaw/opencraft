package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initRepo(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "keep.txt")
	run("commit", "-qm", "init")
}

func TestRootFindsRepoAncestor(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Root(sub); got != root {
		t.Fatalf("Root(%s) = %q, want %q", sub, got, root)
	}
	if got := Root(t.TempDir()); got != "" {
		t.Fatalf("non-repo Root = %q, want empty", got)
	}
}

func TestChangedPaths(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	ctx := context.Background()
	if got := ChangedPaths(ctx, root, ChangedOptions{}); len(got) != 0 {
		t.Fatalf("clean repo changed paths = %v", got)
	}
	for _, name := range []string{"keep.txt", "staged.txt", "new.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("git", "-C", root, "add", "staged.txt")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	got := ChangedPaths(ctx, root, ChangedOptions{})
	want := map[string]bool{"keep.txt": true, "staged.txt": true, "new.txt": true}
	if len(got) != len(want) {
		t.Fatalf("changed paths = %v, want %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Fatalf("unexpected changed path %q", p)
		}
	}
}

func TestChangedPathsNonRepo(t *testing.T) {
	got := ChangedPaths(context.Background(), t.TempDir(), ChangedOptions{})
	if got != nil {
		t.Fatalf("non-repo changed paths = %v, want nil", got)
	}
}

func TestRunBoundedTruncates(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	for i := 0; i < 100; i++ {
		name := filepath.Join(root, "many", "file-"+string(rune('a'+i%26))+"-"+string(rune('0'+i/26)))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, truncated := RunBounded(context.Background(), root, 32, 5_000_000_000,
		"status", "--porcelain", "--untracked-files=all"); !truncated {
		t.Fatal("large status output was not truncated")
	}
}
