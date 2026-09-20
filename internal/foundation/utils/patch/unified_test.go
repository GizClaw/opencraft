package patch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyToDirUnifiedDiff: a patch copied out of `git diff` applies
// through the same engine as the codex envelope, including a context line
// whose own content starts with a tab.
func TestApplyToDirUnifiedDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.go")
	if err := os.WriteFile(path, []byte(
		"package store\n"+
			"\n"+
			"func stats() {\n"+
			"\tif err := run(); err != nil {\n"+
			"\t\treturn err\n"+
			"\t}\n"+
			"\treturn nil\n"+
			"}\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := `diff --git a/stats.go b/stats.go
index 1111111..2222222 100644
--- a/stats.go
+++ b/stats.go
@@ -3,5 +3,6 @@ func stats() {
 	if err := run(); err != nil {
-		return err
+		return wrap(err)
+		// unreachable
 	}
 	return nil
 }
`
	results, err := ApplyToDir(dir, diff)
	if err != nil {
		t.Fatalf("ApplyToDir: %v", err)
	}
	if len(results) != 1 || results[0].Path != "stats.go" ||
		results[0].Action != "update" {
		t.Fatalf("results = %+v", results)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "\t\treturn wrap(err)") ||
		strings.Contains(got, "\t\treturn err\n") {
		t.Fatalf("patched file:\n%s", got)
	}
}

// TestApplyToDirUnifiedDiffPureInsertion: a hunk with no context relies
// on the header's insertion point ("@@ -2,0 +3,1 @@").
func TestApplyToDirUnifiedDiffPureInsertion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := `--- a/notes.txt
+++ b/notes.txt
@@ -2,0 +3,1 @@
+three
`
	if _, err := ApplyToDir(dir, diff); err != nil {
		t.Fatalf("ApplyToDir: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("file = %q", data)
	}
}

// TestDiffRendersUnifiedDiff checks the chat card path: a unified diff
// renders with real line numbers and add/delete counts.
func TestDiffRendersUnifiedDiff(t *testing.T) {
	content := "one\ntwo\nthree\n"
	diff := `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 one
-two
+TWO
 three
`
	files, err := Diff(diff, testReadFile(map[string]string{
		"notes.txt": content,
	}))
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	fd := files[0]
	if fd.Path != "notes.txt" || fd.Action != "update" ||
		fd.Added != 1 || fd.Removed != 1 || len(fd.Lines) != 4 {
		t.Fatalf("file diff = %+v", fd)
	}
	last := fd.Lines[len(fd.Lines)-1]
	if last.Kind != DiffLineContext || last.Text != "three" ||
		last.OldNum != 3 || last.NewNum != 3 {
		t.Fatalf("trailing context = %+v", last)
	}
}

// TestDiffUnifiedDiffNewAndDeletedFiles maps /dev/null headers onto the
// add and delete actions.
func TestDiffUnifiedDiffNewAndDeletedFiles(t *testing.T) {
	added := `diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+first
+second
`
	files, err := Diff(added, testReadFile(nil))
	if err != nil {
		t.Fatalf("Diff add: %v", err)
	}
	if len(files) != 1 || files[0].Action != "add" ||
		files[0].Added != 2 || files[0].Path != "new.txt" {
		t.Fatalf("add diff = %+v", files)
	}

	deleted := `diff --git a/old.txt b/old.txt
deleted file mode 100644
--- a/old.txt
+++ /dev/null
@@ -1,1 +0,0 @@
-gone
`
	files, err = Diff(deleted, testReadFile(map[string]string{
		"old.txt": "gone\n",
	}))
	if err != nil {
		t.Fatalf("Diff delete: %v", err)
	}
	if len(files) != 1 || files[0].Action != "delete" ||
		files[0].Removed != 1 || files[0].Path != "old.txt" {
		t.Fatalf("delete diff = %+v", files)
	}
}

// TestParseUnifiedDiffRejectsUnsupportedOps keeps the failure loud for
// shapes the engine cannot honor instead of silently applying half of a
// patch.
func TestParseUnifiedDiffRejectsUnsupportedOps(t *testing.T) {
	rename := `diff --git a/old.txt b/new.txt
similarity index 100%
rename from old.txt
rename to new.txt
`
	if _, err := ParseAny(rename); err == nil ||
		!strings.Contains(err.Error(), "rename") {
		t.Fatalf("rename error = %v", err)
	}
	binary := `diff --git a/img.png b/img.png
Binary files a/img.png and b/img.png differ
`
	if _, err := ParseAny(binary); err == nil ||
		!strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary error = %v", err)
	}
}

// TestLooksLikeUnifiedDiffIsConservative: envelope patches must never be
// routed to the unified parser.
func TestLooksLikeUnifiedDiffIsConservative(t *testing.T) {
	if LooksLikeUnifiedDiff("*** Begin Patch\n*** Update File: a\n@@\n-x\n+y\n") {
		t.Fatal("codex envelope detected as unified diff")
	}
	if !LooksLikeUnifiedDiff("--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n") {
		t.Fatal("minimal unified diff not detected")
	}
}
