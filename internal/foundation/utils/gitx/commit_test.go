package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitFilesAndDiff(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	ctx := context.Background()

	writeFile(t, filepath.Join(root, "a.txt"), "a1\na2\na3\n")
	writeFile(t, filepath.Join(root, "b.txt"), "b1\nb2\n")
	writeFile(t, filepath.Join(root, "c.bin"), "\x00\x01\x02")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "seed files")
	seed := strings.TrimSpace(gitRun(t, root, "rev-parse", "HEAD"))

	if err := appendFile(root, "a.txt", "a4\n"); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "rm", "-q", "b.txt")
	gitRun(t, root, "commit", "-qam", "tweak files")
	tweak := strings.TrimSpace(gitRun(t, root, "rev-parse", "HEAD"))

	gitRun(t, root, "mv", "a.txt", "renamed.txt")
	gitRun(t, root, "commit", "-qm", "rename file")
	rename := strings.TrimSpace(gitRun(t, root, "rev-parse", "HEAD"))

	rootFiles := CommitFiles(ctx, root, seed)
	if rootFiles.Truncated || len(rootFiles.Files) != 3 {
		t.Fatalf("root CommitFiles = %+v, want 3 files", rootFiles)
	}
	byPath := map[string]CommitFile{}
	for _, f := range rootFiles.Files {
		byPath[f.Path] = f
	}
	if a := byPath["a.txt"]; a.Kind != KindAdded ||
		a.Additions != 3 || a.Deletions != 0 {
		t.Fatalf("root a.txt = %+v", a)
	}
	if c := byPath["c.bin"]; !c.IsBinary || c.Kind != KindAdded {
		t.Fatalf("root c.bin = %+v", c)
	}

	tweakFiles := CommitFiles(ctx, root, tweak)
	if len(tweakFiles.Files) != 2 {
		t.Fatalf("tweak CommitFiles = %+v, want 2 files", tweakFiles)
	}
	byPath = map[string]CommitFile{}
	for _, f := range tweakFiles.Files {
		byPath[f.Path] = f
	}
	if a := byPath["a.txt"]; a.Kind != KindModified || a.Additions != 1 {
		t.Fatalf("tweak a.txt = %+v", a)
	}
	if b := byPath["b.txt"]; b.Kind != KindDeleted || b.Deletions != 2 {
		t.Fatalf("tweak b.txt = %+v", b)
	}

	renameFiles := CommitFiles(ctx, root, rename)
	if len(renameFiles.Files) != 1 ||
		renameFiles.Files[0].Kind != KindRenamed ||
		renameFiles.Files[0].OrigPath != "a.txt" ||
		renameFiles.Files[0].Path != "renamed.txt" {
		t.Fatalf("rename CommitFiles = %+v", renameFiles)
	}

	diff, truncated := CommitDiff(ctx, root, tweak, "a.txt", 0)
	if truncated || !strings.Contains(diff, "+a4") {
		t.Fatalf("CommitDiff(a.txt) truncated=%v diff=%q", truncated, diff)
	}
	deleted, _ := CommitDiff(ctx, root, tweak, "b.txt", 0)
	if !strings.Contains(deleted, "-b2") {
		t.Fatalf("CommitDiff(b.txt) = %q", deleted)
	}
	rootDiff, _ := CommitDiff(ctx, root, seed, "a.txt", 0)
	if !strings.Contains(rootDiff, "+a1") {
		t.Fatalf("root CommitDiff(a.txt) = %q", rootDiff)
	}

	if got := CommitFiles(ctx, root, "0000000000000000000000000000000000000000"); len(got.Files) != 0 {
		t.Fatalf("unknown commit returned files %+v", got.Files)
	}
	if bad, _ := CommitDiff(ctx, root, "not-a-commit", "a.txt", 0); bad != "" {
		t.Fatalf("CommitDiff with invalid oid returned %q", bad)
	}
}

func TestCommitFilesMergeUsesFirstParent(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	gitRun(t, root, "branch", "-m", "main")
	ctx := context.Background()

	gitRun(t, root, "switch", "-qc", "side")
	writeFile(t, filepath.Join(root, "side.txt"), "side\n")
	gitRun(t, root, "add", "side.txt")
	gitRun(t, root, "commit", "-qm", "side change")

	gitRun(t, root, "switch", "-q", "main")
	writeFile(t, filepath.Join(root, "main.txt"), "main\n")
	gitRun(t, root, "add", "main.txt")
	gitRun(t, root, "commit", "-qm", "main change")
	gitRun(t, root, "merge", "-q", "--no-ff", "side", "-m", "merge side")
	merge := strings.TrimSpace(gitRun(t, root, "rev-parse", "HEAD"))

	res := CommitFiles(ctx, root, merge)
	if len(res.Files) != 1 || res.Files[0].Path != "side.txt" ||
		res.Files[0].Kind != KindAdded {
		t.Fatalf("merge CommitFiles = %+v, want only side.txt vs first parent",
			res.Files)
	}
}

func appendFile(root, name, content string) error {
	f, err := os.OpenFile(filepath.Join(root, name),
		os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
