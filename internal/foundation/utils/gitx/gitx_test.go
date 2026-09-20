package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/testing/logcapture"
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

// TestRunBoundedFailureCarriesContext pins the diagnosability of a bare
// "exit status 128": the record has to name the repository and the argv,
// otherwise the warning cannot be traced back to a workspace.
func TestRunBoundedFailureCarriesContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	capture := logcapture.Install(t)
	if _, truncated := RunBounded(context.Background(), root, 4096,
		10*time.Second, "rev-parse", "--show-toplevel"); truncated {
		t.Fatal("failed run must not report truncation")
	}
	for _, record := range capture.Records() {
		if record.Body().AsString() != "gitx: git command failed" {
			continue
		}
		if got := logcapture.Attribute(record, "git.root"); got != root {
			t.Fatalf("git.root = %q, want %q", got, root)
		}
		want := "rev-parse --show-toplevel"
		if got := logcapture.Attribute(record, "git.args"); got != want {
			t.Fatalf("git.args = %q, want %q", got, want)
		}
		return
	}
	t.Fatal("missing gitx: git command failed record")
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
