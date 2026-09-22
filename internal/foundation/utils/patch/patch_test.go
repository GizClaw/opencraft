package patch

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/workspace"
)

// TestHunkMismatchExplainsWhy: the old error only echoed the anchor,
// which is empty for the common failure and told the model nothing.
func TestHunkMismatchExplainsWhy(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)
	if err := ws.Write(ctx, "a.txt", []byte("one\ntwo\n")); err != nil {
		t.Fatal(err)
	}

	// A context line that no longer exists in the file.
	ops, err := Parse("*** Begin Patch\n*** Update File: a.txt\n@@\n" +
		"-missing line\n+new line\n*** End Patch\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, ws, ops); err == nil {
		t.Fatal("mismatched hunk accepted")
	} else {
		msg := err.Error()
		for _, want := range []string{
			`apply_patch: hunk 1 in "a.txt" did not match`,
			`no match for the first context line "missing line"`,
			"re-read the file",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q missing %q", msg, want)
			}
		}
	}

	// An insertion hunk with neither an anchor nor context can never
	// match: say so instead of printing an empty anchor.
	ops, err = Parse("*** Begin Patch\n*** Update File: a.txt\n@@\n" +
		"+new line\n*** End Patch\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, ws, ops); err == nil {
		t.Fatal("anchorless insertion accepted")
	} else if !strings.Contains(err.Error(),
		"insertion hunk has no anchor and no context lines") {
		t.Fatalf("error = %v", err)
	}
}

// memWorkspace roots a LocalWorkspace in a temp dir and closes it before
// that directory is removed.
//
// The top-level workspace pins its root with an open directory handle
// (core's os.Root) and only Close releases it; Windows refuses to remove
// a directory while such a handle is live, which shows up as a TempDir
// cleanup failure rather than as a test failure. t.Cleanup runs in LIFO
// order, so registering the close after TempDir is what runs it first.
func memWorkspace(t *testing.T) workspace.Workspace {
	t.Helper()
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Errorf("close workspace: %v", err)
		}
	})
	return ws
}

func TestApplyAddUpdateDelete(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)

	// Add.
	ops, err := Parse(`*** Begin Patch
*** Add File: hello.txt
+hello
+world
*** End Patch
`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := Apply(ctx, ws, ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != "add" {
		t.Fatalf("results = %+v", results)
	}
	data, err := ws.Read(ctx, "hello.txt")
	if err != nil || string(data) != "hello\nworld\n" {
		t.Fatalf("read = %q err=%v", data, err)
	}

	// Update.
	ops, err = Parse(`*** Begin Patch
*** Update File: hello.txt
@@ hello
-hello
+hi
 world
*** End Patch
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, ws, ops); err != nil {
		t.Fatal(err)
	}
	data, _ = ws.Read(ctx, "hello.txt")
	if string(data) != "hi\nworld\n" {
		t.Fatalf("after update = %q", data)
	}

	// Delete.
	ops, err = Parse(`*** Begin Patch
*** Delete File: hello.txt
*** End Patch
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, ws, ops); err != nil {
		t.Fatal(err)
	}
	if exists, _ := ws.Exists(ctx, "hello.txt"); exists {
		t.Fatal("file should be deleted")
	}
}

func TestApplyInsertionHunk(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)
	if err := ws.Write(ctx, "a.txt", []byte("line1\nline2\n")); err != nil {
		t.Fatal(err)
	}
	ops, err := Parse(`*** Begin Patch
*** Update File: a.txt
@@ line2
+inserted
*** End Patch
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, ws, ops); err != nil {
		t.Fatal(err)
	}
	data, _ := ws.Read(ctx, "a.txt")
	if string(data) != "line1\nline2\ninserted\n" {
		t.Fatalf("after insertion = %q", data)
	}
}

func TestApplyRejectsUnsafePaths(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)
	for _, patch := range []string{
		"*** Begin Patch\n*** Add File: /etc/passwd\n+x\n*** End Patch\n",
		"*** Begin Patch\n*** Add File: ../escape.txt\n+x\n*** End Patch\n",
		// The traversal and absolute forms spelled for Windows, plus the
		// root-relative and UNC ones. Folding the separator is what makes
		// these fail on Windows, where filepath.Clean leaves "..\x"
		// looking like an ordinary name.
		"*** Begin Patch\n*** Add File: ..\\escape.txt\n+x\n*** End Patch\n",
		"*** Begin Patch\n*** Add File: a\\..\\..\\escape.txt\n+x\n*** End Patch\n",
		"*** Begin Patch\n*** Delete File: \\escape.txt\n*** End Patch\n",
		"*** Begin Patch\n*** Add File: \\\\server\\share\\escape.txt\n+x\n*** End Patch\n",
	} {
		if _, err := Parse(patch); err == nil {
			t.Fatalf("expected parse error for %q", patch)
		}
	}
	if _, err := Apply(ctx, ws, []*op{}); err != nil {
		t.Fatal(err)
	}
}

// TestValidatePathJudgesTheSlashNamespace pins both sides of the rule:
// a traversal or an absolute path is refused whichever separator spells
// it, and a plain Windows-style relative path still parses.
//
// The acceptance half is deliberate. Folding the separator is for
// judging only; a patch naming src\main.go keeps working on Windows,
// where the workspace reads the backslash as a separator itself. The
// hardening refuses escapes, not a spelling that already resolved.
func TestValidatePathJudgesTheSlashNamespace(t *testing.T) {
	rejected := []string{
		"..",
		"../escape.txt",
		"a/../../escape.txt",
		`..\escape.txt`,
		`..\..\escape.txt`,
		`a\..\..\escape.txt`,
		"/etc/passwd",
		`\escape.txt`,
		`\\server\share\escape.txt`,
		`src/..\..\escape.txt`,
	}
	for _, p := range rejected {
		if err := validatePath(p); err == nil {
			t.Errorf("validatePath(%q) = nil, want rejection", p)
		}
	}
	accepted := []string{
		"a.txt",
		"src/main.go",
		`src\main.go`,
		`nested\deep/file.go`,
	}
	for _, p := range accepted {
		if err := validatePath(p); err != nil {
			t.Errorf("validatePath(%q) = %v, want accept", p, err)
		}
	}
}

func TestApplyErrors(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)

	// Add existing.
	if err := ws.Write(ctx, "x.txt", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	ops, _ := Parse("*** Begin Patch\n*** Add File: x.txt\n+y\n*** End Patch\n")
	if _, err := Apply(ctx, ws, ops); err == nil {
		t.Fatal("expected conflict on add existing")
	}

	// Update no match.
	ops, _ = Parse("*** Begin Patch\n*** Update File: x.txt\n@@ nope\n-nope\n+y\n*** End Patch\n")
	if _, err := Apply(ctx, ws, ops); err == nil {
		t.Fatal("expected error on unmatched hunk")
	}

	// Delete missing.
	ops, _ = Parse("*** Begin Patch\n*** Delete File: missing.txt\n*** End Patch\n")
	if _, err := Apply(ctx, ws, ops); err == nil {
		t.Fatal("expected error deleting missing file")
	}
}
