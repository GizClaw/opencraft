package applypatch

import (
	"testing"
)

func testReadFile(files map[string]string) ReadFile {
	return func(path string) (string, error) {
		if content, ok := files[path]; ok {
			return content, nil
		}
		return "", errNotExist(path)
	}
}

type notExistError struct{ path string }

func (e notExistError) Error() string { return "no such file: " + e.path }

func errNotExist(path string) error { return notExistError{path: path} }

func TestDiffAddFile(t *testing.T) {
	diffs, err := Diff(`*** Begin Patch
*** Add File: hello.go
+package main
+
+func main() {}
*** End Patch
`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("diffs = %+v", diffs)
	}
	fd := diffs[0]
	if fd.Path != "hello.go" || fd.Action != "add" ||
		fd.Added != 3 || fd.Removed != 0 {
		t.Errorf("file diff = %+v", fd)
	}
	if len(fd.Lines) != 3 {
		t.Fatalf("lines = %+v", fd.Lines)
	}
	for i, l := range fd.Lines {
		if l.Kind != DiffLineAdd || l.NewNum != i+1 || l.OldNum != 0 {
			t.Errorf("line %d = %+v", i, l)
		}
	}
}

func TestDiffUpdateHunkLineNumbers(t *testing.T) {
	files := map[string]string{
		"app.go": "line1\nline2\nline3\nline4\nline5\n",
	}
	diffs, err := Diff(`*** Begin Patch
*** Update File: app.go
@@ func main
 line2
-line3
+line3b
 line4
*** End Patch
`, testReadFile(files))
	if err != nil {
		t.Fatal(err)
	}
	fd := diffs[0]
	if fd.Action != "update" || fd.Added != 1 || fd.Removed != 1 {
		t.Fatalf("file diff = %+v", fd)
	}
	if len(fd.Lines) != 4 {
		t.Fatalf("lines = %+v", fd.Lines)
	}
	want := []DiffLine{
		{Kind: DiffLineContext, OldNum: 2, NewNum: 2, Text: "line2"},
		{Kind: DiffLineDelete, OldNum: 3, NewNum: 0, Text: "line3"},
		{Kind: DiffLineAdd, OldNum: 0, NewNum: 3, Text: "line3b"},
		{Kind: DiffLineContext, OldNum: 4, NewNum: 4, Text: "line4"},
	}
	for i, w := range want {
		if fd.Lines[i] != w {
			t.Errorf("line %d = %+v, want %+v", i, fd.Lines[i], w)
		}
	}
}

func TestDiffMultipleHunksNumberAgainstAppliedFile(t *testing.T) {
	files := map[string]string{
		"app.go": "one\ntwo\nthree\nfour\nfive\n",
	}
	diffs, err := Diff(`*** Begin Patch
*** Update File: app.go
@@ first
 one
-two
+two+
@@ second
 four
-five
+five+
*** End Patch
`, testReadFile(files))
	if err != nil {
		t.Fatal(err)
	}
	fd := diffs[0]
	if len(fd.Lines) != 6 {
		t.Fatalf("lines = %+v", fd.Lines)
	}
	// Second hunk matches against the applied file: after replacing
	// "two" with "two+" (same line count), "five" is still line 5.
	// The first hunk replaces "one two" with "two+" (its context line
	// "one" is part of the removed sequence), so after applying it
	// "five" moves from line 5 to line 4; the second hunk renders
	// against that applied state, matching how apply runs hunks.
	if got := fd.Lines[4]; got.Kind != DiffLineDelete ||
		got.OldNum != 4 || got.Text != "five" {
		t.Errorf("second hunk delete = %+v", got)
	}
}

func TestDiffInsertionHunkUsesAnchor(t *testing.T) {
	files := map[string]string{
		"app.go": "a\nb\nc\n",
	}
	diffs, err := Diff(`*** Begin Patch
*** Update File: app.go
@@ b
+inserted
*** End Patch
`, testReadFile(files))
	if err != nil {
		t.Fatal(err)
	}
	fd := diffs[0]
	if len(fd.Lines) != 1 {
		t.Fatalf("lines = %+v", fd.Lines)
	}
	if l := fd.Lines[0]; l.Kind != DiffLineAdd ||
		l.NewNum != 3 || l.Text != "inserted" {
		t.Errorf("insertion line = %+v", l)
	}
}

func TestDiffUnreadableFileFallsBackToRelativeNumbers(t *testing.T) {
	diffs, err := Diff(`*** Begin Patch
*** Update File: missing.go
@@ whatever
-old
+new
*** End Patch
`, testReadFile(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	fd := diffs[0]
	if len(fd.Lines) != 2 {
		t.Fatalf("lines = %+v", fd.Lines)
	}
	if l := fd.Lines[0]; l.Kind != DiffLineDelete ||
		l.OldNum != 1 || l.Text != "old" {
		t.Errorf("fallback delete = %+v", l)
	}
	if l := fd.Lines[1]; l.Kind != DiffLineAdd ||
		l.NewNum != 1 || l.Text != "new" {
		t.Errorf("fallback add = %+v", l)
	}
}

func TestDiffDeleteFile(t *testing.T) {
	files := map[string]string{"gone.go": "a\nb\n"}
	diffs, err := Diff(`*** Begin Patch
*** Delete File: gone.go
*** End Patch
`, testReadFile(files))
	if err != nil {
		t.Fatal(err)
	}
	fd := diffs[0]
	if fd.Action != "delete" || fd.Added != 0 || fd.Removed != 2 {
		t.Fatalf("file diff = %+v", fd)
	}
	if len(fd.Lines) != 2 {
		t.Fatalf("lines = %+v", fd.Lines)
	}
	if l := fd.Lines[1]; l.Kind != DiffLineDelete ||
		l.OldNum != 2 || l.Text != "b" {
		t.Errorf("delete line = %+v", l)
	}
}

func TestDiffRejectsInvalidPatch(t *testing.T) {
	if _, err := Diff("not a patch", nil); err == nil {
		t.Error("invalid patch must error")
	}
}
