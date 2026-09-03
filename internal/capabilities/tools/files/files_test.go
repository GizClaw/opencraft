package files

import (
	"context"
	"strings"
	"testing"

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

func execute(t *testing.T, tool execTool, args string) (string, error) {
	t.Helper()
	return tool.Execute(context.Background(), args)
}

type execTool interface {
	Execute(context.Context, string) (string, error)
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

// helpers returning concrete tools -------------------------------------------

func (t *Tool) read() execTool  { return &readFileTool{t.ws} }
func (t *Tool) write() execTool { return &writeFileTool{t.ws} }
func (t *Tool) list() execTool  { return &listDirTool{t.ws} }
func (t *Tool) grep() execTool  { return &grepTool{t.ws} }
func (t *Tool) glob() execTool  { return &globTool{t.ws} }
