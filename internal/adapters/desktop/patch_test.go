package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenderPatchDiffLineNumbers(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "src", "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := renderPatchDiff(root, `*** Begin Patch
*** Update File: src/main.go
@@
 line1
-line2
+line2 changed
 line3
*** End Patch
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "src/main.go" {
		t.Fatalf("files = %+v", files)
	}
	lines := files[0].Lines
	want := []struct {
		kind   string
		oldNum int
		newNum int
		text   string
	}{
		{"context", 1, 1, "line1"},
		{"delete", 2, 0, "line2"},
		{"add", 0, 2, "line2 changed"},
		{"context", 3, 3, "line3"},
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %+v", lines)
	}
	for i, w := range want {
		got := lines[i]
		if got.Kind != w.kind ||
			got.OldNum != w.oldNum ||
			got.NewNum != w.newNum ||
			got.Text != w.text {
			t.Fatalf("line[%d] = %+v, want %+v", i, got, w)
		}
	}
}

func TestRenderPatchDiffMissingFile(t *testing.T) {
	root := t.TempDir()
	files, err := renderPatchDiff(root, `*** Begin Patch
*** Update File: missing.txt
@@
-old
+new
*** End Patch
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "missing.txt" {
		t.Fatalf("files = %+v", files)
	}
	// Unreadable files still render with hunk-relative numbers.
	if len(files[0].Lines) != 2 {
		t.Fatalf("lines = %+v", files[0].Lines)
	}
}
