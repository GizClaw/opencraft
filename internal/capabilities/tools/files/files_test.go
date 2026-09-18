package files

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/workspace"
)

func newTestTool(t *testing.T) (*Tool, workspace.Workspace) {
	t.Helper()
	root := t.TempDir()
	ws, err := workspace.NewLocalWorkspace(root)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	tool, err := New(ws)
	if err != nil {
		t.Fatalf("files.New: %v", err)
	}
	return tool, ws
}

func writeTree(t *testing.T, ws workspace.Workspace, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := ws.Write(context.Background(), name, []byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

type execTool interface {
	Execute(context.Context, string) (message.Content, error)
}

func execute(t *testing.T, tool execTool, args string) (string, error) {
	t.Helper()
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		return "", err
	}
	return out.Text(), nil
}

func TestReadFileRange(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{
		"a.txt": "one\ntwo\nthree\nfour\n",
	})
	got, err := execute(t, tool.read(), `{"file_path":"a.txt","offset":2,"limit":2}`)
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	for _, want := range []string{`"content":"two\nthree\n"`, `"offset":2`, `"limit":2`, `"total_lines":4`, `"is_truncated":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("read_file result missing %s: %s", want, got)
		}
	}
}

// TestReadFilePointsImagesAtViewImage: binary image bytes are not text,
// so read_file answers with a pointer to view_image instead of handing
// the model a mojibake string. The bytes decide, so the pointer follows
// a picture saved under a text name too.
func TestReadFilePointsImagesAtViewImage(t *testing.T) {
	tool, _ := newTestTool(t)
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13}
	writeTree(t, tool.ws, map[string]string{})
	for _, name := range []string{"shot.png", "notes.md"} {
		if err := tool.ws.Write(context.Background(), name, png); err != nil {
			t.Fatalf("write image: %v", err)
		}
		got, err := execute(t, tool.read(), `{"file_path":"`+name+`"}`)
		if err != nil {
			t.Fatalf("read_file: %v", err)
		}
		if !strings.Contains(got, "view_image") {
			t.Fatalf("read_file %s result = %q, want a view_image pointer",
				name, got)
		}
	}
}

func TestReadFileFullAndMissing(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{"a.txt": "only\n"})
	got, err := execute(t, tool.read(), `{"file_path":"a.txt"}`)
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if !strings.Contains(got, `"total_lines":1`) || !strings.Contains(got, `"is_truncated":false`) {
		t.Errorf("read_file full: %s", got)
	}
	if _, err := execute(t, tool.read(), `{"file_path":"missing.txt"}`); err == nil {
		t.Error("read_file missing should error")
	}
	if _, err := execute(t, tool.read(), `{"file_path":"/etc/passwd"}`); err == nil {
		t.Error("read_file absolute path should be rejected")
	}
}

func TestWriteFileCreatesParentsAndOverwrites(t *testing.T) {
	tool, ws := newTestTool(t)
	got, err := execute(t, tool.write(), `{"file_path":"deep/nested/f.txt","content":"hello\n"}`)
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !strings.Contains(got, `"bytes":6`) {
		t.Errorf("write_file result: %s", got)
	}
	data, err := ws.Read(context.Background(), "deep/nested/f.txt")
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("read back: %q, %v", data, err)
	}
	if _, err := execute(t, tool.write(), `{"file_path":"deep/nested/f.txt","content":"bye"}`); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if _, err := execute(t, tool.write(), `{"file_path":"../escape.txt","content":"x"}`); err == nil {
		t.Error("write_file traversal should be rejected")
	}
}

// TestFileToolsNameUnknownArguments: a misspelled argument must name
// the key the model sent instead of silently defaulting (or answering
// "required" for a key it did send).
func TestFileToolsNameUnknownArguments(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{"a.txt": "one\n"})
	_, err := execute(t, tool.read(), `{"file_path":"a.txt","maximum":1}`)
	if err == nil {
		t.Fatal("unknown argument accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		"read_file: unknown argument \"maximum\"",
		"accepted arguments:",
		"file_path",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

// TestGrepPathMustBeDirectory: grep walks directories; handing it a
// file used to surface the raw walk error ("workspace: list …").
func TestGrepPathMustBeDirectory(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{"src/main.go": "package main\n"})
	_, err := execute(t, tool.grep(), `{"pattern":"main","path":"src/main.go"}`)
	if err == nil {
		t.Fatal("grep on a file path accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		`grep: path "src/main.go" is a file, not a directory`,
		"read the file instead",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestListDirRecursiveAndHidden(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{
		"src/main.go":   "package main",
		"src/util/x.go": "package util",
		".hidden/h.go":  "package hidden",
		".git/config":   "ignored",
	})
	got, err := execute(t, tool.list(), `{"path":".","recursive":true}`)
	if err != nil {
		t.Fatalf("list_dir: %v", err)
	}
	for _, want := range []string{`"path":"src/main.go"`, `"path":"src/util/x.go"`, `"type":"dir"`} {
		if !strings.Contains(got, want) {
			t.Errorf("list_dir result missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, ".hidden") || strings.Contains(got, ".git") {
		t.Errorf("list_dir should skip hidden/.git: %s", got)
	}
	gotHidden, err := execute(t, tool.list(), `{"path":".","recursive":true,"include_hidden":true}`)
	if err != nil {
		t.Fatalf("list_dir hidden: %v", err)
	}
	if !strings.Contains(gotHidden, ".hidden/h.go") || strings.Contains(gotHidden, ".git/config") {
		t.Errorf("list_dir hidden semantics: %s", gotHidden)
	}
}

func TestListDirOnFileRejected(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{"a.txt": "hi\n"})
	if _, err := execute(t, tool.list(), `{"path":"a.txt"}`); err == nil {
		t.Fatal("list_dir on a file should error")
	} else if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("list_dir file error should mention not a directory: %v", err)
	}
}

func TestGrepFixedRegexAndCase(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{
		"a.txt": "Hello World\nfoo bar\n",
		"b.txt": "hello there\n",
	})
	got, err := execute(t, tool.grep(), `{"pattern":"hello","case_insensitive":true}`)
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	for _, want := range []string{`"path":"a.txt"`, `"line_number":1`, `"path":"b.txt"`} {
		if !strings.Contains(got, want) {
			t.Errorf("grep result missing %s: %s", want, got)
		}
	}
	gotFixed, err := execute(t, tool.grep(), `{"pattern":"hello","fixed_strings":true}`)
	if err != nil {
		t.Fatalf("grep fixed: %v", err)
	}
	if strings.Contains(gotFixed, "Hello") {
		t.Errorf("grep fixed should be case-sensitive: %s", gotFixed)
	}
	if _, err := execute(t, tool.grep(), `{"pattern":""}`); err == nil {
		t.Error("grep empty pattern should error")
	}
}

func TestGlobDoubleStar(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{
		"a_test.go":         "package a",
		"pkg/b_test.go":     "package b",
		"pkg/sub/c_test.go": "package c",
		"pkg/sub/readme.md": "docs",
	})
	got, err := execute(t, tool.glob(), `{"pattern":"**/*_test.go"}`)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, want := range []string{`"a_test.go"`, `"pkg/b_test.go"`, `"pkg/sub/c_test.go"`} {
		if !strings.Contains(got, want) {
			t.Errorf("glob result missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "readme.md") {
		t.Errorf("glob should not match readme.md: %s", got)
	}
	gotSingle, err := execute(t, tool.glob(), `{"pattern":"pkg/*.go"}`)
	if err != nil {
		t.Fatalf("glob single star: %v", err)
	}
	if strings.Contains(gotSingle, "sub/") || !strings.Contains(gotSingle, "b_test.go") {
		t.Errorf("glob single star semantics: %s", gotSingle)
	}
}

// trackingWorkspace records whether reads went through the bounded
// LimitedReader path or the whole-file Read path.
type trackingWorkspace struct {
	workspace.Workspace
	fullReads    int
	limitedReads int
}

func (w *trackingWorkspace) Read(ctx context.Context, path string) ([]byte, error) {
	w.fullReads++
	return w.Workspace.Read(ctx, path)
}

func (w *trackingWorkspace) ReadLimited(
	ctx context.Context, path string, maxBytes int64,
) ([]byte, error) {
	w.limitedReads++
	return w.Workspace.(workspace.LimitedReader).ReadLimited(ctx, path, maxBytes)
}

func TestReadFileUsesLimitedRead(t *testing.T) {
	inner, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tracked := &trackingWorkspace{Workspace: inner}
	tool, err := New(tracked)
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", maxReadFileBytes+1)
	if err := inner.Write(context.Background(), "big.txt", []byte(large)); err != nil {
		t.Fatal(err)
	}

	if _, err := execute(t, tool.read(), `{"file_path":"big.txt"}`); err == nil {
		t.Fatal("read_file should reject an oversized file")
	}
	if tracked.fullReads != 0 {
		t.Fatalf("read_file used %d whole-file reads, want 0", tracked.fullReads)
	}
	if tracked.limitedReads == 0 {
		t.Fatal("read_file did not use ReadLimited")
	}
}

func TestGrepUsesLimitedReadAndSkipsLargeFiles(t *testing.T) {
	inner, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tracked := &trackingWorkspace{Workspace: inner}
	tool, err := New(tracked)
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", maxGrepFileBytes+1)
	if err := inner.Write(context.Background(), "big.txt", []byte(large)); err != nil {
		t.Fatal(err)
	}
	if err := inner.Write(context.Background(), "small.txt", []byte("needle\n")); err != nil {
		t.Fatal(err)
	}

	got, err := execute(t, tool.grep(), `{"pattern":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"skipped_large":1`) {
		t.Errorf("grep skipped_large not reported: %s", got)
	}
	if tracked.fullReads != 0 {
		t.Fatalf("grep used %d whole-file reads, want 0", tracked.fullReads)
	}
	if tracked.limitedReads == 0 {
		t.Fatal("grep did not use ReadLimited")
	}
}

func TestGrepStopsAfterFileScanBudget(t *testing.T) {
	old := maxGrepFiles
	maxGrepFiles = 10
	t.Cleanup(func() { maxGrepFiles = old })

	inner, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, err := New(inner)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("f%02d.txt", i)
		if err := inner.Write(context.Background(), name, []byte("no match\n")); err != nil {
			t.Fatal(err)
		}
	}

	got, err := execute(t, tool.grep(), `{"pattern":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"truncated":true`) {
		t.Errorf("grep should stop at the scan budget: %s", got)
	}
}

// helpers returning concrete tools -------------------------------------------

func (t *Tool) read() execTool  { return &readFileTool{t.ws} }
func (t *Tool) write() execTool { return &writeFileTool{t.ws} }
func (t *Tool) list() execTool  { return &listDirTool{t.ws} }
func (t *Tool) grep() execTool  { return &grepTool{t.ws} }
func (t *Tool) glob() execTool  { return &globTool{t.ws} }
