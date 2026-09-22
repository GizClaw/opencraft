package gitx

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func marksOf(t *testing.T, root, path string) MarkSet {
	t.Helper()
	return FileMarks(context.Background(), root, path)
}

func wantRanges(t *testing.T, name string, got, want []LineRange) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: ranges = %+v, want %+v", name, got, want)
	}
}

func wantDels(t *testing.T, name string, got, want []DeleteAnchor) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: deletions = %+v, want %+v", name, got, want)
	}
}

// TestParseHunksFixtures pins the four header shapes `git diff -U0`
// emits, including the omitted counts and the trailing function-context
// hint, plus the count arithmetic behind the chip's +N/-M.
func TestParseHunksFixtures(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/f.txt b/f.txt",
		"index 1b1906a..b9043ab 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -0,0 +1 @@",
		"+new0",
		"@@ -4 +5 @@ l3",
		"-l4",
		"+l4x",
		"@@ -8,2 +8,0 @@ l6",
		"-l7",
		"-l8",
		"@@ -20,0 +21,3 @@ func foo() {",
		"+a",
		"+b",
		"+c",
		"\\ No newline at end of file",
	}, "\n")
	adds, mods, dels, additions, deletions := parseHunks(diff)
	wantRanges(t, "adds", adds, []LineRange{
		{Start: 1, Count: 1},
		{Start: 21, Count: 3},
	})
	wantRanges(t, "mods", mods, []LineRange{{Start: 5, Count: 1}})
	wantDels(t, "dels", dels, []DeleteAnchor{
		{After: 8, Count: 2, OldStart: 8},
	})
	// Both counted sides: the replacement hunk adds one line as well, so
	// the totals stay the `git diff --numstat` ones.
	if additions != 5 || deletions != 3 {
		t.Fatalf("counts = +%d -%d, want +5 -3", additions, deletions)
	}
}

func TestFileMarksCleanAndIgnored(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeFile(t, filepath.Join(root, "clean.txt"), "one\ntwo\n")
	gitRun(t, root, "add", "clean.txt")
	gitRun(t, root, "commit", "-qm", "clean")
	writeFile(t, filepath.Join(root, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(root, "noise.log"), "noise\n")

	if got := marksOf(t, root, "clean.txt"); got.Kind != "" || got.Adds != nil {
		t.Fatalf("clean file marks = %+v, want an empty snapshot", got)
	}
	if got := marksOf(t, root, "noise.log"); got.Kind != "" {
		t.Fatalf("ignored file kind = %q, want empty", got.Kind)
	}
	if got := marksOf(t, root, "../escape.txt"); got.Kind != "" {
		t.Fatalf("escaping path kind = %q, want empty", got.Kind)
	}
}

func TestFileMarksUntracked(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeFile(t, filepath.Join(root, "fresh.txt"), "a\nb\n")

	got := marksOf(t, root, "fresh.txt")
	if got.Kind != KindUntracked || !got.Untracked {
		t.Fatalf("untracked marks = %+v, want kind untracked", got)
	}
	if got.Adds != nil || got.Mods != nil || got.Dels != nil {
		t.Fatalf("untracked marks carry ranges: %+v", got)
	}
}

// TestFileMarksStagedAndUnstaged pins the coordinate choice: the merged
// diff reports the working-tree line numbers, so the unstaged insertion
// moves the staged change from line 4 to line 5.
func TestFileMarksStagedAndUnstaged(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeFile(t, filepath.Join(root, "f.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n")
	gitRun(t, root, "add", "f.txt")
	gitRun(t, root, "commit", "-qm", "f")

	writeFile(t, filepath.Join(root, "f.txt"), "l1\nl2\nl3\nl4x\nl5\nl6\nl7\nl8\n")
	gitRun(t, root, "add", "f.txt")
	writeFile(t, filepath.Join(root, "f.txt"),
		"new0\nl1\nl2\nl3\nl4x\nl5\nl6\nl7\nl8\n")

	got := marksOf(t, root, "f.txt")
	if got.Kind != KindModified || !got.Staged || !got.Unstaged {
		t.Fatalf("marks = %+v, want modified with both sides", got)
	}
	wantRanges(t, "adds", got.Adds, []LineRange{{Start: 1, Count: 1}})
	wantRanges(t, "mods", got.Mods, []LineRange{{Start: 5, Count: 1}})
	// The totals match `git diff --numstat HEAD`: the replacement line
	// counts as an addition as well.
	if got.Additions != 2 || got.Deletions != 1 {
		t.Fatalf("counts = +%d -%d, want +2 -1", got.Additions, got.Deletions)
	}
}

func TestFileMarksDeletions(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeFile(t, filepath.Join(root, "f.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n")
	gitRun(t, root, "add", "f.txt")
	gitRun(t, root, "commit", "-qm", "f")

	cases := []struct {
		name  string
		body  string
		want  []DeleteAnchor
		lines int
	}{
		{
			name:  "middle",
			body:  "l1\nl2\nl5\nl6\nl7\nl8\n",
			want:  []DeleteAnchor{{After: 2, Count: 2, OldStart: 3}},
			lines: 2,
		},
		{
			name:  "head",
			body:  "l3\nl4\nl5\nl6\nl7\nl8\n",
			want:  []DeleteAnchor{{After: 0, Count: 2, OldStart: 1}},
			lines: 2,
		},
		{
			name:  "tail",
			body:  "l1\nl2\nl3\nl4\nl5\nl6\n",
			want:  []DeleteAnchor{{After: 6, Count: 2, OldStart: 7}},
			lines: 2,
		},
		{
			name:  "whole file",
			body:  "",
			want:  []DeleteAnchor{{After: 0, Count: 8, OldStart: 1}},
			lines: 8,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, filepath.Join(root, "f.txt"), tc.body)
			got := marksOf(t, root, "f.txt")
			wantDels(t, "dels", got.Dels, tc.want)
			if got.Adds != nil || got.Mods != nil {
				t.Fatalf("deletion-only marks carry ranges: %+v", got)
			}
			if got.Deletions != tc.lines || got.Additions != 0 {
				t.Fatalf("counts = +%d -%d, want +0 -%d",
					got.Additions, got.Deletions, tc.lines)
			}
		})
	}
}

func TestFileMarksNewFileWithoutCommits(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.email", "test@example.com")
	gitRun(t, root, "config", "user.name", "test")
	writeFile(t, filepath.Join(root, "first.txt"), "a\nb\nc\n")
	gitRun(t, root, "add", "first.txt")

	got := marksOf(t, root, "first.txt")
	if got.Kind != KindAdded {
		t.Fatalf("kind = %q, want added", got.Kind)
	}
	wantRanges(t, "adds", got.Adds, []LineRange{{Start: 1, Count: 3}})
	if got.Additions != 3 || got.Deletions != 0 {
		t.Fatalf("counts = +%d -%d, want +3 -0", got.Additions, got.Deletions)
	}
}

func TestFileMarksBinary(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeFile(t, filepath.Join(root, "b.dat"), "one\n")
	gitRun(t, root, "add", "b.dat")
	gitRun(t, root, "commit", "-qm", "binary")
	if err := os.WriteFile(filepath.Join(root, "b.dat"),
		[]byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}

	got := marksOf(t, root, "b.dat")
	if !got.Binary || got.Kind != KindModified {
		t.Fatalf("binary marks = %+v, want a binary modification", got)
	}
	if got.Adds != nil || got.Mods != nil || got.Dels != nil {
		t.Fatalf("binary marks carry ranges: %+v", got)
	}
}

// TestFileMarksRenameDiffsBothSides pins the rename pairing: without the
// source path in the pathspec git reports the destination as a brand new
// file and every line reads as added.
func TestFileMarksRenameDiffsBothSides(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeFile(t, filepath.Join(root, "r1.txt"), "p\nq\nr\ns\n")
	gitRun(t, root, "add", "r1.txt")
	gitRun(t, root, "commit", "-qm", "ren")

	gitRun(t, root, "mv", "r1.txt", "r2.txt")
	writeFile(t, filepath.Join(root, "r2.txt"), "p\nq\nR\ns\n")

	got := marksOf(t, root, "r2.txt")
	if got.Kind != KindRenamed || got.OrigPath != "r1.txt" {
		t.Fatalf("rename marks = %+v, want renamed from r1.txt", got)
	}
	wantRanges(t, "mods", got.Mods, []LineRange{{Start: 3, Count: 1}})
	if got.Adds != nil {
		t.Fatalf("rename added ranges = %+v, want none", got.Adds)
	}
}

func TestFileMarksPathSpecials(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	rel := "app/[slug] ✓.txt"
	writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), "a\nb\nc\n")
	gitRun(t, root, "add", "--", rel)
	gitRun(t, root, "commit", "-qm", "specials")

	writeFile(t, filepath.Join(root, filepath.FromSlash(rel)), "a\nB\nc\n")
	got := marksOf(t, root, rel)
	if got.Kind != KindModified {
		t.Fatalf("special-path kind = %q, want modified", got.Kind)
	}
	wantRanges(t, "mods", got.Mods, []LineRange{{Start: 2, Count: 1}})
}

func TestFileMarksTruncation(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeFile(t, filepath.Join(root, "f.txt"), "l1\nl2\nl3\nl4\n")
	gitRun(t, root, "add", "f.txt")
	gitRun(t, root, "commit", "-qm", "f")
	writeFile(t, filepath.Join(root, "f.txt"), "l1\nl2\nL3\nl4\n")

	origDiff, origRanges := marksDiffLimit, marksMaxRanges
	t.Cleanup(func() {
		marksDiffLimit, marksMaxRanges = origDiff, origRanges
	})

	marksDiffLimit = 32
	got := marksOf(t, root, "f.txt")
	if !got.Truncated || got.Mods != nil {
		t.Fatalf("diff-truncated marks = %+v, want truncated without ranges", got)
	}
	if got.Kind != KindModified {
		t.Fatalf("truncated kind = %q, want the status kind", got.Kind)
	}

	marksDiffLimit = origDiff
	marksMaxRanges = 0
	got = marksOf(t, root, "f.txt")
	if !got.Truncated || got.Mods != nil {
		t.Fatalf("range-capped marks = %+v, want truncated without ranges", got)
	}
	if got.Additions != 1 || got.Deletions != 1 {
		t.Fatalf("range-capped counts = +%d -%d, want +1 -1",
			got.Additions, got.Deletions)
	}
}

// TestFileMarksFallsBackWhenStatusIsCapped pins the second lookup: a
// snapshot that dropped entries must not turn a changed file into a
// clean one.
func TestFileMarksFallsBackWhenStatusIsCapped(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	for _, name := range []string{"a.txt", "m.txt", "z.txt"} {
		writeFile(t, filepath.Join(root, name), "one\n")
	}
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-qm", "many")
	for _, name := range []string{"a.txt", "m.txt", "z.txt"} {
		writeFile(t, filepath.Join(root, name), "one\ntwo\n")
	}

	origPaths := marksStatusPaths
	t.Cleanup(func() { marksStatusPaths = origPaths })
	marksStatusPaths = 1

	got := marksOf(t, root, "z.txt")
	if got.Kind != KindModified {
		t.Fatalf("capped-snapshot kind = %q, want modified", got.Kind)
	}
	wantRanges(t, "adds", got.Adds, []LineRange{{Start: 2, Count: 1}})
}

func TestFileMarksEmptyInputs(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	if got := FileMarks(context.Background(), "", "f.txt"); got.Kind != "" {
		t.Fatalf("empty root marks = %+v, want empty", got)
	}
	if got := FileMarks(context.Background(), root, ""); got.Kind != "" {
		t.Fatalf("empty path marks = %+v, want empty", got)
	}
}
