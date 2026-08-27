package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initGitRepo(t *testing.T, root string) {
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

func TestGitSnapshot(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	ctx := context.Background()

	// No changes yet: empty snapshot.
	if got := gitSnapshot(ctx, root); len(got) != 0 {
		t.Fatalf("clean repo snapshot = %+v, want empty", got)
	}

	// Modify one file, stage another, add an untracked file.
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "staged.txt"), []byte("s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "add", "staged.txt")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	got := gitSnapshot(ctx, root)
	byPath := make(map[string]string, len(got))
	for _, st := range got {
		if st.Skipped {
			t.Errorf("unexpected skipped file %s", st.Path)
		}
		byPath[st.Path] = st.Content
	}
	for _, want := range []struct {
		path, content string
	}{
		{"keep.txt", "changed\n"},
		{"staged.txt", "s\n"},
		{"new.txt", "n\n"},
	} {
		if gotContent, ok := byPath[want.path]; !ok || gotContent != want.content {
			t.Errorf("snapshot[%s] = %q, %v; want %q", want.path, gotContent, ok, want.content)
		}
	}
}

func TestGitSnapshotNonRepo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitSnapshot(context.Background(), root); len(got) != 0 {
		t.Fatalf("non-repo snapshot = %+v, want empty", got)
	}
}
