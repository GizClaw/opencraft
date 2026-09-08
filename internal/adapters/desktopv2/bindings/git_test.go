package bindings

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func gitInTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeInTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initRepoInTest(t *testing.T, root string) {
	t.Helper()
	gitInTest(t, root, "init", "-q", "-b", "main")
	gitInTest(t, root, "config", "user.email", "test@example.com")
	gitInTest(t, root, "config", "user.name", "test")
	writeInTest(t, filepath.Join(root, "keep.txt"), "keep\n")
	gitInTest(t, root, "add", "keep.txt")
	gitInTest(t, root, "commit", "-qm", "init")
}

func newGitBinding(t *testing.T, workDir string) *Git {
	t.Helper()
	c := core.NewCore(t.TempDir(), t.TempDir(), workDir)
	c.Shell.SetContext(context.Background())
	return NewGitBinding(c)
}

func TestGitRepoOutsideRepository(t *testing.T) {
	b := newGitBinding(t, t.TempDir())
	repo := b.Repo()
	if repo.InRepo {
		t.Fatalf("Repo = %+v, want not in repo", repo)
	}
	if _, err := b.Status(); err == nil {
		t.Fatal("Status outside a repo unexpectedly succeeded")
	}
}

func TestGitStatusUsesFullRepository(t *testing.T) {
	root := t.TempDir()
	initRepoInTest(t, root)
	workspace := filepath.Join(root, "work")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeInTest(t, filepath.Join(workspace, "in.txt"), "a\nb\nc\n")
	gitInTest(t, root, "add", "work/in.txt")
	writeInTest(t, filepath.Join(root, "keep.txt"), "keep\nchanged\n")

	b := newGitBinding(t, workspace)
	repo := b.Repo()
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.InRepo || repo.Root != wantRoot {
		t.Fatalf("Repo = %+v, want root %q", repo, wantRoot)
	}
	status, err := b.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch == "" || status.Root != wantRoot {
		t.Fatalf("Status header = %+v", status)
	}
	var inside, outside bool
	for _, e := range status.Entries {
		switch e.Path {
		case "work/in.txt":
			inside = e.InWorkspace && e.Staged && e.Additions == 3
		case "keep.txt":
			outside = !e.InWorkspace && e.Unstaged && e.Additions == 1
		}
	}
	if !inside || !outside {
		t.Fatalf("Status entries = %+v, want inside work/in.txt and outside keep.txt",
			status.Entries)
	}
}

func TestGitReadMethods(t *testing.T) {
	root := t.TempDir()
	initRepoInTest(t, root)
	writeInTest(t, filepath.Join(root, "extra.txt"), "extra\n")
	gitInTest(t, root, "add", "extra.txt")
	gitInTest(t, root, "commit", "-qm", "add extra")
	writeInTest(t, filepath.Join(root, "extra.txt"), "extra\nmore\n")
	gitInTest(t, root, "branch", "side")

	b := newGitBinding(t, root)
	logs, err := b.Log(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || logs[0].Subject != "add extra" || logs[0].OID == "" {
		t.Fatalf("Log = %+v", logs)
	}
	branches, err := b.Branches()
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 {
		t.Fatalf("Branches = %+v, want 2", branches)
	}
	diff, err := b.Diff("extra.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Content, "+more") {
		t.Fatalf("Diff = %q, want +more hunk", diff.Content)
	}
	if _, err := b.Diff("missing.txt", false); err != nil {
		t.Fatal(err)
	}
}

func TestGitWriteOperations(t *testing.T) {
	root := t.TempDir()
	initRepoInTest(t, root)
	dataDir := t.TempDir()
	c := core.NewCore(t.TempDir(), dataDir, root)
	b := NewGitBinding(c)

	writeInTest(t, filepath.Join(root, "keep.txt"), "keep\nui edit\n")
	if _, err := b.Stage([]string{"keep.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Commit("ui commit"); err != nil {
		t.Fatal(err)
	}
	log := gitInTest(t, root, "log", "-1", "--format=%s")
	if strings.TrimSpace(log) != "ui commit" {
		t.Fatalf("commit subject = %q", log)
	}
	layout, err := config.ResolveWorkspace(dataDir, root)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := os.ReadFile(filepath.Join(layout.AuditDir, "git-ops.jsonl"))
	if err != nil {
		t.Fatalf("audit trail missing: %v", err)
	}
	if !strings.Contains(string(audit), `"kind":"commit"`) {
		t.Fatalf("audit missing commit record: %s", audit)
	}

	if _, err := b.NewBranch("ui-branch"); err != nil {
		t.Fatal(err)
	}
	current := gitInTest(t, root, "branch", "--show-current")
	if strings.TrimSpace(current) != "ui-branch" {
		t.Fatalf("current branch = %q", current)
	}
	if _, err := b.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	writeInTest(t, filepath.Join(root, "untracked.txt"), "x\n")
	if _, err := b.Clean([]string{"untracked.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "untracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("clean did not remove untracked.txt: %v", err)
	}
}
