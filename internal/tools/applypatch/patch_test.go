package applypatch

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/workspace"
)

func memWorkspace(t *testing.T) workspace.Workspace {
	t.Helper()
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
	} {
		if _, err := Parse(patch); err == nil {
			t.Fatalf("expected parse error for %q", patch)
		}
	}
	if _, err := Apply(ctx, ws, []*op{}); err != nil {
		t.Fatal(err)
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

func TestToolDefinition(t *testing.T) {
	tool, err := New(memWorkspace(t))
	if err != nil {
		t.Fatal(err)
	}
	def := tool.Definition()
	if def.Name != Name || !strings.Contains(def.Description, "Begin Patch") {
		t.Fatalf("definition = %+v", def)
	}
	if !tool.Metadata().MutatesState {
		t.Fatal("apply_patch must be mutating")
	}
}
