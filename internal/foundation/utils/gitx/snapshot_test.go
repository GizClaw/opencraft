package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInfoFindsRepoRootAncestor(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	ctx := context.Background()
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	info := Info(ctx, sub)
	if info.Root != wantRoot {
		t.Fatalf("Info(sub).Root = %q, want %q", info.Root, wantRoot)
	}
	if info.Branch == "" || info.Branch == "HEAD" {
		t.Fatalf("Info(sub).Branch = %q, want a real branch", info.Branch)
	}
	if got := Info(ctx, t.TempDir()); got.Root != "" {
		t.Fatalf("non-repo Info.Root = %q, want empty", got.Root)
	}
}

func TestStatusSnapshot(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	ctx := context.Background()

	writeFile(t, filepath.Join(root, "staged.txt"), "one\ntwo\nthree\n")
	gitRun(t, root, "add", "staged.txt")

	if err := os.WriteFile(filepath.Join(root, "bin.dat"),
		[]byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "bin.dat")

	writeFile(t, filepath.Join(root, "old name.txt"),
		"alpha\nbeta\ngamma\n")
	gitRun(t, root, "add", "old name.txt")
	gitRun(t, root, "commit", "-qm", "add rename source", "--", "old name.txt")
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "mv", "old name.txt", "dir/new name.txt")
	writeFile(t, filepath.Join(root, "dir", "new name.txt"),
		"alpha\nbeta\ngamma\ndelta\n")
	gitRun(t, root, "add", "dir/new name.txt")

	writeFile(t, filepath.Join(root, "keep.txt"), "keep2\nkeep3\n")
	writeFile(t, filepath.Join(root, "utd", "u1.txt"), "u\n")

	res := Status(ctx, root, StatusOptions{})
	if res.Truncated {
		t.Fatal("small repo unexpectedly truncated")
	}
	byPath := make(map[string]Entry, len(res.Entries))
	for _, e := range res.Entries {
		byPath[e.Path] = e
	}

	staged := byPath["staged.txt"]
	if !staged.Staged || staged.Unstaged || staged.Kind != KindAdded {
		t.Fatalf("staged.txt = %+v, want staged added", staged)
	}
	if staged.Additions != 3 || staged.Deletions != 0 {
		t.Fatalf("staged.txt counts = +%d/-%d, want +3/-0",
			staged.Additions, staged.Deletions)
	}

	bin := byPath["bin.dat"]
	if !bin.Staged || !bin.IsBinary {
		t.Fatalf("bin.dat = %+v, want staged binary", bin)
	}

	renamed := byPath["dir/new name.txt"]
	if !renamed.Staged || renamed.Kind != KindRenamed {
		t.Fatalf("renamed = %+v, want staged rename", renamed)
	}
	if renamed.OrigPath != "old name.txt" {
		t.Fatalf("renamed.OrigPath = %q, want %q", renamed.OrigPath, "old name.txt")
	}
	if renamed.Additions != 1 || renamed.Deletions != 0 {
		t.Fatalf("renamed counts = +%d/-%d, want +1/-0",
			renamed.Additions, renamed.Deletions)
	}

	modified := byPath["keep.txt"]
	if !modified.Unstaged || modified.Staged || modified.Kind != KindModified {
		t.Fatalf("keep.txt = %+v, want unstaged modified", modified)
	}
	if modified.Additions != 2 || modified.Deletions != 1 {
		t.Fatalf("keep.txt counts = +%d/-%d, want +2/-1",
			modified.Additions, modified.Deletions)
	}

	utd := byPath["utd"]
	if !utd.Untracked || !utd.Directory || utd.Path != "utd" {
		t.Fatalf("utd = %+v, want collapsed untracked directory", utd)
	}
}

func TestStatusUnmerged(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	ctx := context.Background()
	writeFile(t, filepath.Join(root, "f.txt"), "base\n")
	gitRun(t, root, "add", "f.txt")
	gitRun(t, root, "commit", "-qm", "base file")
	current := gitRun(t, root, "rev-parse", "--abbrev-ref", "HEAD")
	current = trimOutput(current)

	gitRun(t, root, "checkout", "-qb", "side")
	writeFile(t, filepath.Join(root, "f.txt"), "side\n")
	gitRun(t, root, "add", "f.txt")
	gitRun(t, root, "commit", "-qm", "side change")

	gitRun(t, root, "checkout", "-q", current)
	writeFile(t, filepath.Join(root, "f.txt"), "main\n")
	gitRun(t, root, "add", "f.txt")
	gitRun(t, root, "commit", "-qm", "main change")

	// The merge conflicts and leaves unmerged index entries.
	cmd := exec.Command("git", "-C", root, "merge", "side")
	_ = cmd.Run()

	res := Status(ctx, root, StatusOptions{})
	found := false
	for _, e := range res.Entries {
		if e.Path == "f.txt" && e.Unmerged && e.Kind == KindUnmerged {
			found = true
		}
	}
	if !found {
		t.Fatalf("no unmerged entry for f.txt in %+v", res.Entries)
	}
}

func TestLog(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	ctx := context.Background()
	writeFile(t, filepath.Join(root, "x.txt"), "x\n")
	gitRun(t, root, "add", "x.txt")
	gitRun(t, root, "commit", "-qm", "second subject")

	entries, truncated := Log(ctx, root, 1)
	if truncated || len(entries) != 1 {
		t.Fatalf("Log = %+v truncated=%v", entries, truncated)
	}
	head := trimOutput(gitRun(t, root, "rev-parse", "HEAD"))
	if entries[0].OID != head || entries[0].Subject != "second subject" {
		t.Fatalf("Log entry = %+v, want %s / second subject", entries[0], head)
	}
	if entries[0].ShortOID == "" || entries[0].Author == "" ||
		entries[0].Date == "" {
		t.Fatalf("Log entry missing fields: %+v", entries[0])
	}
}

func TestBranches(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	ctx := context.Background()
	gitRun(t, root, "branch", "side")

	branches := Branches(ctx, root)
	var current, side string
	for _, b := range branches {
		if b.Name == "side" {
			side = b.Name
		}
		if b.Current {
			current = b.Name
		}
	}
	if side == "" || current == "" || current == side {
		t.Fatalf("Branches = %+v, want current + side", branches)
	}
}

func TestRefTracksUpstream(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	ctx := context.Background()
	bare := filepath.Join(t.TempDir(), "remote.git")
	if err := exec.Command("git", "init", "-q", "--bare", bare).Run(); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "remote", "add", "origin", bare)
	gitRun(t, root, "push", "-qu", "origin", "HEAD")
	writeFile(t, filepath.Join(root, "extra.txt"), "x\n")
	gitRun(t, root, "add", "extra.txt")
	gitRun(t, root, "commit", "-qm", "extra")

	r := Ref(ctx, root)
	if r.Branch == "" {
		t.Fatal("Ref.Branch empty")
	}
	if !strings.HasSuffix(r.Upstream, "/"+r.Branch) {
		t.Fatalf("Ref.Upstream = %q, want tracking %q", r.Upstream, r.Branch)
	}
	if r.Ahead != 1 || r.Behind != 0 {
		t.Fatalf("Ref counts = ahead %d behind %d, want 1/0",
			r.Ahead, r.Behind)
	}
}

func TestDiff(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	ctx := context.Background()
	writeFile(t, filepath.Join(root, "keep.txt"), "first\nsecond\n")

	out, truncated := Diff(ctx, root, "keep.txt", false, 0)
	if truncated || out == "" {
		t.Fatalf("Diff empty: truncated=%v out=%q", truncated, out)
	}
	if _, ok := Diff(ctx, root, "../escape.txt", false, 0); ok {
		t.Fatal("Diff accepted an escaping path")
	}
	if diff, _ := Diff(ctx, root, "new.txt", false, 0); diff != "" {
		t.Fatalf("untracked Diff = %q, want empty", diff)
	}
}

func TestIsSafePath(t *testing.T) {
	for _, ok := range []string{
		"a.txt", "dir/a b.txt", "./a.txt",
	} {
		if !isSafePath(ok) {
			t.Fatalf("isSafePath(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{
		"", "/abs/path", "..", "../up.txt", "a/../../up.txt",
	} {
		if isSafePath(bad) {
			t.Fatalf("isSafePath(%q) = true, want false", bad)
		}
	}
}

func trimOutput(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
