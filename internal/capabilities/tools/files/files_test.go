package files

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/workspace"
)

func newTestTool(t *testing.T) (*Tool, workspace.Workspace) {
	t.Helper()
	ws := newTestWorkspace(t)
	tool, err := New(ws)
	if err != nil {
		t.Fatalf("files.New: %v", err)
	}
	return tool, ws
}

// newTestWorkspace roots a LocalWorkspace in a temp dir and closes it
// before that directory is removed.
//
// The top-level workspace pins its root with an open directory handle
// (core's os.Root) and only Close releases it; Windows refuses to remove
// a directory while such a handle is live, which shows up as a TempDir
// cleanup failure rather than as a test failure. t.Cleanup runs in LIFO
// order, so registering the close after TempDir is what runs it first.
func newTestWorkspace(t *testing.T) *workspace.LocalWorkspace {
	t.Helper()
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Errorf("close workspace: %v", err)
		}
	})
	return ws
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

// TestListDirMaxDepthOnNestedDirs pins the depth arithmetic on the
// workspace-path convention: a nested directory is deeper than its parent
// on every platform, so max_depth trims it. Counting the host separator
// would read every path as depth 0 and this walk would run to the leaf.
func TestListDirMaxDepthOnNestedDirs(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{
		"top.txt":                "top",
		"src/one.go":             "package src",
		"src/deep/two.go":        "package deep",
		"src/deep/more/three.go": "package more",
	})
	got, err := execute(t, tool.list(), `{"path":".","recursive":true,"max_depth":1}`)
	if err != nil {
		t.Fatalf("list_dir: %v", err)
	}
	for _, want := range []string{"top.txt", "src/one.go", "src/deep/two.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("list_dir max_depth=1 missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "three.go") {
		t.Errorf("list_dir max_depth=1 walked into depth 2: %s", got)
	}
}

// TestDepthBelowOnWorkspacePaths is the same contract at the arithmetic
// level, for the root-relative and subdirectory-relative forms.
func TestDepthBelowOnWorkspacePaths(t *testing.T) {
	for _, tc := range []struct {
		root, path string
		want       int
	}{
		{".", "a.go", 0},
		{".", "src/a.go", 1},
		{".", "src/deep/a.go", 2},
		{"src", "src/a.go", 0},
		{"src", "src/deep/a.go", 1},
		{"src/", "src/deep/a.go", 1},
	} {
		if got := depthBelow(tc.root, tc.path); got != tc.want {
			t.Errorf("depthBelow(%q, %q) = %d, want %d",
				tc.root, tc.path, got, tc.want)
		}
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

// TestGlobAppliesPatternUnderPath pins the meaning of path: the pattern
// is relative to it. Matching the pattern against workspace-wide paths
// and afterwards filtering by a "<path>/" prefix could only ever return
// nothing for a pattern that does not literally start with that prefix,
// so {pattern:"*.go", path:"pkg"} silently answered "no files".
func TestGlobAppliesPatternUnderPath(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{
		"root.go":      "package root",
		"pkg/b.go":     "package pkg",
		"pkg/sub/c.go": "package sub",
		"other/d.go":   "package other",
	})

	got, err := execute(t, tool.glob(), `{"pattern":"*.go","path":"pkg"}`)
	if err != nil {
		t.Fatalf("glob under path: %v", err)
	}
	if !strings.Contains(got, `"pkg/b.go"`) {
		t.Errorf("glob under path missed the direct child: %s", got)
	}
	for _, unwanted := range []string{"sub", "other", "root.go"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("glob under path leaked %q: %s", unwanted, got)
		}
	}

	deep, err := execute(t, tool.glob(), `{"pattern":"**/*.go","path":"pkg"}`)
	if err != nil {
		t.Fatalf("glob deep under path: %v", err)
	}
	for _, want := range []string{`"pkg/b.go"`, `"pkg/sub/c.go"`} {
		if !strings.Contains(deep, want) {
			t.Errorf("glob deep under path missing %s: %s", want, deep)
		}
	}
	if strings.Contains(deep, "other/") {
		t.Errorf("glob deep under path escaped the base: %s", deep)
	}

	if _, err := execute(t, tool.glob(), `{"pattern":"*.go","path":"../pkg"}`); err == nil {
		t.Error("glob accepted an escaping path")
	}
}

func TestGlobReportsTruncationOnlyWhenItCuts(t *testing.T) {
	old := maxGlobMatches
	maxGlobMatches = 2
	t.Cleanup(func() { maxGlobMatches = old })

	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{
		"a.go": "package a",
		"b.go": "package b",
		"c.go": "package c",
	})

	got, err := execute(t, tool.glob(), `{"pattern":"*.go"}`)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if !strings.Contains(got, `"truncated":true`) {
		t.Errorf("glob dropped a path without reporting truncation: %s", got)
	}
	var result struct {
		Matches   []string `json:"matches"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("glob result is not JSON: %v", err)
	}
	if n := len(result.Matches); n != maxGlobMatches {
		t.Errorf("glob returned %d paths, want the cap %d: %s", n, maxGlobMatches, got)
	}

	toolSmall, _ := newTestTool(t)
	writeTree(t, toolSmall.ws, map[string]string{"only.go": "package only"})
	full, err := execute(t, toolSmall.glob(), `{"pattern":"*.go"}`)
	if err != nil {
		t.Fatalf("glob under the cap: %v", err)
	}
	if !strings.Contains(full, `"truncated":false`) {
		t.Errorf("glob called a complete result truncated: %s", full)
	}
}

// TestGrepReportsTruncationOnlyWhenTheWalkStops pins the other half of
// the same rule: truncation means a hit was dropped, so a hit count
// that lands exactly on the cap is the complete answer it looks like.
func TestGrepReportsTruncationOnlyWhenTheWalkStops(t *testing.T) {
	old := maxGrepMatches
	maxGrepMatches = 2
	t.Cleanup(func() { maxGrepMatches = old })

	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{
		"a.txt": "needle one",
		"b.txt": "needle two",
	})
	got, err := execute(t, tool.grep(), `{"pattern":"needle"}`)
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if strings.Contains(got, `"truncated":true`) {
		t.Errorf("grep called a complete result truncated: %s", got)
	}
	var result struct {
		Matches   []grepMatch `json:"matches"`
		Truncated bool        `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("grep result is not JSON: %v", err)
	}
	if len(result.Matches) != 2 {
		t.Errorf("grep returned %d matches, want both: %s", len(result.Matches), got)
	}

	toolMore, _ := newTestTool(t)
	writeTree(t, toolMore.ws, map[string]string{
		"a.txt": "needle one",
		"b.txt": "needle two",
		"c.txt": "needle three",
	})
	more, err := execute(t, toolMore.grep(), `{"pattern":"needle"}`)
	if err != nil {
		t.Fatalf("grep over the cap: %v", err)
	}
	if !strings.Contains(more, `"truncated":true`) {
		t.Errorf("grep stopped at the cap without saying so: %s", more)
	}
	if err := json.Unmarshal([]byte(more), &result); err != nil {
		t.Fatalf("grep result is not JSON: %v", err)
	}
	if len(result.Matches) != 2 {
		t.Errorf("grep returned %d matches, want the cap 2: %s", len(result.Matches), more)
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
	inner := newTestWorkspace(t)
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
	inner := newTestWorkspace(t)
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

	inner := newTestWorkspace(t)
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

// TestGrepSkipsBinaryAndOversizedBeforeReading pins what a grep costs
// per file: the walk's own stat retires a file that is over the size
// bound before its bytes are read, and binary content (a NUL byte in
// the head) is skipped instead of being copied into a string and split
// into lines. Reading a MiB of every bundle and asset under the
// workspace only to discard it was the bulk of the 4.4GB of io.ReadAll
// a heap profile showed under this tool.
func TestGrepSkipsBinaryAndOversizedBeforeReading(t *testing.T) {
	inner := newTestWorkspace(t)
	tracked := &trackingWorkspace{Workspace: inner}
	tool, err := New(tracked)
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.Write(context.Background(), "big.txt",
		[]byte(strings.Repeat("x", maxGrepFileBytes+1))); err != nil {
		t.Fatal(err)
	}
	if err := inner.Write(context.Background(), "bundle.wasm",
		[]byte("\x00\x61\x73\x6dneedle")); err != nil {
		t.Fatal(err)
	}
	if err := inner.Write(context.Background(), "asset.png",
		[]byte("\x89PNG\x00\x00nothing here")); err != nil {
		t.Fatal(err)
	}
	if err := inner.Write(context.Background(), "small.txt",
		[]byte("needle\n")); err != nil {
		t.Fatal(err)
	}

	got, err := execute(t, tool.grep(), `{"pattern":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"skipped_large":1`) {
		t.Errorf("grep skipped_large not reported: %s", got)
	}
	// The matching binary file is still reported — by path, not by
	// line — and the non-matching one is not.
	if !strings.Contains(got, `"binary_matches":["bundle.wasm"]`) {
		t.Errorf("grep binary_matches not reported: %s", got)
	}
	if !strings.Contains(got, `"path":"small.txt"`) {
		t.Errorf("grep missed the text file: %s", got)
	}
	// The oversized file is retired by its size, so only the two binary
	// files and the text file are read.
	if tracked.limitedReads != 3 {
		t.Errorf("grep read %d files, want 3 (oversized skipped by stat)",
			tracked.limitedReads)
	}
}

// TestScanLinesAllocatesNothing pins the line scan itself: a file that
// yields no match must not be copied into a string or split into a
// slice of lines the way strings.Split(string(data), "\n") did.
func TestScanLinesAllocatesNothing(t *testing.T) {
	data := benchData()
	allocs := testing.AllocsPerRun(10, func() {
		scanLines(data, func(int, []byte) bool { return true })
	})
	if allocs > 0 {
		t.Errorf("scanLines allocated %v times per pass, want 0", allocs)
	}
}

// TestScanLinesMatchesSplitSemantics keeps the line numbering and the
// trailing-empty-line behaviour identical to the strings.Split call it
// replaced, including an empty file (one empty line) and a file that
// ends with a newline (a final empty line).
func TestScanLinesMatchesSplitSemantics(t *testing.T) {
	cases := []struct {
		in    string
		lines []string
	}{
		{"", []string{""}},
		{"a", []string{"a"}},
		{"a\n", []string{"a", ""}},
		{"a\nb", []string{"a", "b"}},
		{"\n\n", []string{"", "", ""}},
	}
	for _, tc := range cases {
		var got []string
		scanLines([]byte(tc.in), func(_ int, line []byte) bool {
			got = append(got, string(line))
			return true
		})
		if strings.Join(got, "|") != strings.Join(tc.lines, "|") {
			t.Errorf("scanLines(%q) = %q, want %q",
				tc.in, got, tc.lines)
		}
	}
}

// helpers returning concrete tools -------------------------------------------

func (t *Tool) read() execTool  { return &readFileTool{t.ws} }
func (t *Tool) write() execTool { return &writeFileTool{t.ws} }
func (t *Tool) list() execTool  { return &listDirTool{t.ws} }
func (t *Tool) grep() execTool  { return &grepTool{t.ws} }
func (t *Tool) glob() execTool  { return &globTool{t.ws} }

// readResult is the decoded read_file payload.
type readResult struct {
	Content     string `json:"content"`
	Offset      int    `json:"offset"`
	Limit       int    `json:"limit"`
	TotalLines  int    `json:"total_lines"`
	IsTruncated bool   `json:"is_truncated"`
	NextOffset  int    `json:"next_offset"`
}

func readWindow(t *testing.T, tool *Tool, args string) readResult {
	t.Helper()
	out, err := execute(t, tool.read(), args)
	if err != nil {
		t.Fatalf("read_file %s: %v", args, err)
	}
	var got readResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode read_file result: %v (%s)", err, out)
	}
	return got
}

// longFile builds n lines of a width that makes the character budget,
// not the line limit, the binding constraint.
func longFile(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %04d %s\n", i, strings.Repeat("x", 40))
	}
	return b.String()
}

// TestReadFileStopsAtResultBudget: one read returns a whole window below
// the truncate cap and names where the next one starts. The window has to
// be contiguous — the model pages from next_offset, so a window that
// skipped its middle would leave lines nothing points at.
func TestReadFileStopsAtResultBudget(t *testing.T) {
	tool, _ := newTestTool(t)
	content := longFile(1000)
	writeTree(t, tool.ws, map[string]string{"long.txt": content})

	got := readWindow(t, tool, `{"file_path":"long.txt"}`)
	if !got.IsTruncated {
		t.Fatalf("read_file returned the whole %d-byte file, want a window under the budget", len(content))
	}
	if len(got.Content) > ReadResultChars+len("line 0001 ")+40 {
		t.Fatalf("window = %d chars, want at most the budget plus one line", len(got.Content))
	}
	lines := strings.Split(strings.TrimSuffix(got.Content, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("window = %d lines, want a usable range", len(lines))
	}
	if got.NextOffset != len(lines)+1 {
		t.Fatalf("next_offset = %d, want %d (one past the %d lines returned)",
			got.NextOffset, len(lines)+1, len(lines))
	}
	if want := strings.Split(strings.TrimSuffix(content, "\n"), "\n")[:len(lines)]; strings.Join(want, "\n") != strings.Join(lines, "\n") {
		t.Fatal("window is not the file's first lines")
	}
}

// TestReadFileWindowsTileTheFile: the windows next_offset chains together
// cover every line exactly once, which is what lets a caller read a long
// file without re-reading or skipping.
func TestReadFileWindowsTileTheFile(t *testing.T) {
	tool, _ := newTestTool(t)
	content := longFile(300)
	writeTree(t, tool.ws, map[string]string{"long.txt": content})

	var got []string
	offset := 1
	for reads := 0; ; reads++ {
		if reads > 10 {
			t.Fatal("windows did not finish the file")
		}
		win := readWindow(t, tool, fmt.Sprintf(`{"file_path":"long.txt","offset":%d}`, offset))
		got = append(got, strings.Split(strings.TrimSuffix(win.Content, "\n"), "\n")...)
		if !win.IsTruncated {
			break
		}
		if win.NextOffset <= offset {
			t.Fatalf("next_offset = %d does not advance past offset %d", win.NextOffset, offset)
		}
		offset = win.NextOffset
	}
	if want := strings.Split(strings.TrimSuffix(content, "\n"), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("windows cover %d lines, want the file's %d", len(got), len(want))
	}
}

// TestReadFileKeepsOneOverlongLine: a single line past the budget still
// comes back. Returning nothing would leave the caller with no range to
// page from; the truncate middleware is what bounds a line that long.
func TestReadFileKeepsOneOverlongLine(t *testing.T) {
	tool, _ := newTestTool(t)
	line := strings.Repeat("y", ReadResultChars+5000)
	writeTree(t, tool.ws, map[string]string{"min.js": line + "\n"})

	got := readWindow(t, tool, `{"file_path":"min.js"}`)
	if got.Content != line+"\n" {
		t.Fatalf("window = %d chars, want the single %d-char line", len(got.Content), len(line))
	}
	if got.IsTruncated {
		t.Fatal("a one-line file has no next window")
	}
}

// TestReadFileLimitBelowBudgetWins: an explicit limit still bounds the
// window, and the result keeps pointing at the next offset.
func TestReadFileLimitBelowBudgetWins(t *testing.T) {
	tool, _ := newTestTool(t)
	writeTree(t, tool.ws, map[string]string{"long.txt": longFile(50)})

	got := readWindow(t, tool, `{"file_path":"long.txt","limit":10}`)
	if lines := strings.Count(got.Content, "\n"); lines != 10 {
		t.Fatalf("window = %d lines, want the requested 10", lines)
	}
	if got.NextOffset != 11 {
		t.Fatalf("next_offset = %d, want 11", got.NextOffset)
	}
}
