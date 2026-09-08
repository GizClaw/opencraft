package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoGit(t *testing.T, root string, args ...string) string {
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

func writeRepoFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initRepoPackage(t *testing.T, root string) {
	t.Helper()
	repoGit(t, root, "init", "-q", "-b", "main")
	repoGit(t, root, "config", "user.email", "test@example.com")
	repoGit(t, root, "config", "user.name", "test")
	writeRepoFile(t, filepath.Join(root, "keep.txt"), "keep\n")
	repoGit(t, root, "add", "keep.txt")
	repoGit(t, root, "commit", "-qm", "init")
}

func TestStageCommitDiscardCycle(t *testing.T) {
	root := t.TempDir()
	initRepoPackage(t, root)
	ctx := context.Background()
	auditDir := t.TempDir()
	run := func(op Op) error {
		t.Helper()
		_, err := Run(ctx, Request{Root: root, AuditDir: auditDir, Op: op})
		return err
	}

	writeRepoFile(t, filepath.Join(root, "keep.txt"), "keep\nedited\n")
	writeRepoFile(t, filepath.Join(root, "new.txt"), "new\n")
	if err := run(Op{Kind: KindStage, Paths: []string{"keep.txt", "new.txt"}}); err != nil {
		t.Fatal(err)
	}
	if err := run(Op{Kind: KindCommit, Message: "stage and commit"}); err != nil {
		t.Fatal(err)
	}
	log := repoGit(t, root, "log", "-1", "--format=%s")
	if strings.TrimSpace(log) != "stage and commit" {
		t.Fatalf("log subject = %q", log)
	}

	writeRepoFile(t, filepath.Join(root, "new.txt"), "changed\n")
	if err := run(Op{Kind: KindDiscardWorktree, Paths: []string{"new.txt"}}); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(root, "new.txt")); got != "new\n" {
		t.Fatalf("after discard new.txt = %q", got)
	}

	writeRepoFile(t, filepath.Join(root, "keep.txt"), "keep\nstaged\n")
	if err := run(Op{Kind: KindStage, Paths: []string{"keep.txt"}}); err != nil {
		t.Fatal(err)
	}
	// Unstage keeps the working-tree change.
	if err := run(Op{Kind: KindUnstage, Paths: []string{"keep.txt"}}); err != nil {
		t.Fatal(err)
	}
	staged := strings.TrimSpace(repoGit(t, root, "diff", "--cached", "--name-only"))
	if staged != "" {
		t.Fatalf("staged paths after unstage = %q", staged)
	}
	// DiscardBoth reverts index and working tree.
	if err := run(Op{Kind: KindStage, Paths: []string{"keep.txt"}}); err != nil {
		t.Fatal(err)
	}
	if err := run(Op{Kind: KindDiscardBoth, Paths: []string{"keep.txt"}}); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(root, "keep.txt")); got != "keep\nedited\n" {
		t.Fatalf("after discard-both keep.txt = %q, want the committed state", got)
	}

	data, err := os.ReadFile(filepath.Join(auditDir, "git-ops.jsonl"))
	if err != nil {
		t.Fatalf("audit trail missing: %v", err)
	}
	if !strings.Contains(string(data), `"kind":"commit"`) {
		t.Fatalf("audit trail missing commit record: %s", data)
	}
}

func TestCleanAndBranchOps(t *testing.T) {
	root := t.TempDir()
	initRepoPackage(t, root)
	ctx := context.Background()
	writeRepoFile(t, filepath.Join(root, "junk", "a.txt"), "x\n")
	if _, err := Run(ctx, Request{Root: root, Op: Op{
		Kind: KindClean, Paths: []string{"junk"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "junk")); !os.IsNotExist(err) {
		t.Fatalf("junk dir still exists: %v", err)
	}

	if _, err := Run(ctx, Request{Root: root, Op: Op{
		Kind: KindNewBranch, Branch: "feat/x",
	}}); err != nil {
		t.Fatal(err)
	}
	branch := strings.TrimSpace(repoGit(t, root, "branch", "--show-current"))
	if branch != "feat/x" {
		t.Fatalf("branch = %q, want feat/x", branch)
	}
	if _, err := Run(ctx, Request{Root: root, Op: Op{
		Kind: KindCheckout, Branch: "main",
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestPullPushAndForce(t *testing.T) {
	root := t.TempDir()
	initRepoPackage(t, root)
	ctx := context.Background()
	bare := filepath.Join(t.TempDir(), "remote.git")
	// Pin the bare remote's default branch to main: otherwise the peer
	// clone below can start on the runner's configured default (master
	// on some CI images), push a second branch, and the pull back in
	// root would be a silent no-op.
	if err := exec.Command(
		"git", "init", "-q", "-b", "main", "--bare", bare,
	).Run(); err != nil {
		t.Fatal(err)
	}
	repoGit(t, root, "remote", "add", "origin", bare)
	repoGit(t, root, "push", "-u", "origin", "main")

	// A second clone pushes a change the first repo pulls back.
	peer := t.TempDir()
	repoGit(t, root, "clone", "-q", "-b", "main", bare, peer)
	writeRepoFile(t, filepath.Join(peer, "peer.txt"), "peer\n")
	repoGit(t, peer, "add", "peer.txt")
	repoGit(t, peer, "commit", "-qm", "peer change")
	repoGit(t, peer, "push", "-q", "origin", "HEAD")

	if _, err := Run(ctx, Request{Root: root, Op: Op{Kind: KindPull}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "peer.txt")); err != nil {
		t.Fatalf("pulled peer.txt missing: %v", err)
	}

	writeRepoFile(t, filepath.Join(root, "local.txt"), "local\n")
	repoGit(t, root, "add", "local.txt")
	repoGit(t, root, "commit", "-qm", "local change")
	if _, err := Run(ctx, Request{Root: root, Op: Op{Kind: KindPush}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, Request{Root: root, Op: Op{Kind: KindForcePush}}); err != nil {
		t.Fatal(err)
	}
}

func TestRunBlocksUnmergedAndBadInput(t *testing.T) {
	root := t.TempDir()
	initRepoPackage(t, root)
	ctx := context.Background()
	writeRepoFile(t, filepath.Join(root, "f.txt"), "base\n")
	repoGit(t, root, "add", "f.txt")
	repoGit(t, root, "commit", "-qm", "base")
	repoGit(t, root, "checkout", "-qb", "side")
	writeRepoFile(t, filepath.Join(root, "f.txt"), "side\n")
	repoGit(t, root, "add", "f.txt")
	repoGit(t, root, "commit", "-qm", "side")
	repoGit(t, root, "checkout", "-q", "main")
	writeRepoFile(t, filepath.Join(root, "f.txt"), "main\n")
	repoGit(t, root, "add", "f.txt")
	repoGit(t, root, "commit", "-qm", "main")
	cmd := exec.Command("git", "-C", root, "merge", "side")
	_ = cmd.Run()

	if _, err := Run(ctx, Request{Root: root, Op: Op{
		Kind: KindCommit, Message: "should not commit",
	}}); err == nil || !strings.Contains(err.Error(), "unmerged") {
		t.Fatalf("unmerged commit error = %v", err)
	}
	if _, err := Run(ctx, Request{Root: root, Op: Op{
		Kind: KindStage, Paths: []string{"../escape.txt"},
	}}); err == nil {
		t.Fatal("escaping path unexpectedly accepted")
	}
	if _, err := Run(ctx, Request{Root: root, Op: Op{
		Kind: KindCheckout, Branch: "-evil",
	}}); err == nil {
		t.Fatal("option-like branch unexpectedly accepted")
	}
}

func TestInvalidBranchNamesRejected(t *testing.T) {
	for _, name := range []string{
		"-evil",
		"a/../b",
		"a//b",
		"a.lock",
		"foo.lock/bar",
		"a.b/.x",
		"a@{b}",
		"a/",
		"a.",
		"",
		"has space",
	} {
		if _, err := buildArgs(Op{Kind: KindCheckout, Branch: name}); err == nil {
			t.Errorf("checkout branch %q unexpectedly accepted", name)
		}
		if _, err := buildArgs(Op{Kind: KindNewBranch, Branch: name}); err == nil {
			t.Errorf("new branch %q unexpectedly accepted", name)
		}
	}
	for _, name := range []string{
		"main",
		"feat/panel",
		"fix/1.2.3",
		"renovate/updates",
	} {
		args, err := buildArgs(Op{Kind: KindCheckout, Branch: name})
		if err != nil {
			t.Errorf("valid checkout branch %q rejected: %v", name, err)
			continue
		}
		if len(args) != 2 || args[0] != "switch" {
			t.Errorf("checkout %q args = %v, want git switch", name, args)
		}
	}
}

// TestCheckoutNeverFallsBackToPathspec pins the fail-open regression
// where `git checkout <file>` silently restores an existing path from
// the index when no branch with that name exists. `git switch` rejects
// the argument instead, so working-tree content survives.
func TestCheckoutNeverFallsBackToPathspec(t *testing.T) {
	root := t.TempDir()
	initRepoPackage(t, root)
	ctx := context.Background()
	writeRepoFile(t, filepath.Join(root, "keep.txt"), "keep\nedited\n")

	if _, err := Run(ctx, Request{Root: root, Op: Op{
		Kind: KindCheckout, Branch: "keep.txt",
	}}); err == nil {
		t.Fatal("checkout of a path-shaped name unexpectedly succeeded")
	}
	if got := mustRead(t, filepath.Join(root, "keep.txt")); got != "keep\nedited\n" {
		t.Fatalf("working-tree file was clobbered by checkout: %q", got)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
