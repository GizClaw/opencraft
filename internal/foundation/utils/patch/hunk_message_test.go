package patch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyToDirHunkErrorNamesTheAnchor: an insertion hunk whose "@@"
// anchor is absent used to fail with "(anchor ...)" and nothing else.
func TestApplyToDirHunkErrorNamesTheAnchor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	patchText := "*** Begin Patch\n" +
		"*** Update File: a.txt\n" +
		"@@ anchor that is not there\n" +
		"+added\n" +
		"*** End Patch\n"
	_, err := ApplyToDir(dir, patchText)
	if err == nil {
		t.Fatal("missing anchor must fail")
	}
	msg := err.Error()
	for _, want := range []string{
		`apply_patch: hunk 1 in "a.txt" did not match`,
		`no line contains the "@@" anchor "anchor that is not there"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

// TestHunkErrorQuotesTheFile: a hunk whose context lines were guessed
// from memory used to answer "no match for the first context line" and
// nothing else, so the only way forward was another guess. The error
// now says where the hunk's own lines do appear and quotes the file
// around them.
func TestHunkErrorQuotesTheFile(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)
	file := "package x\n\nfunc a() {\n\tone()\n\ttwo()\n\tthree()\n}\n\nfunc b() {\n}\n"
	if err := ws.Write(ctx, "a.go", []byte(file)); err != nil {
		t.Fatal(err)
	}
	// The first context line no longer exists; the rest still does.
	ops, err := Parse("*** Begin Patch\n*** Update File: a.go\n@@\n" +
		" \tzero()\n \tone()\n-\ttwo()\n+\tTWO()\n*** End Patch\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(ctx, ws, ops)
	if err == nil {
		t.Fatal("stale context line accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		"the hunk's lines no longer line up in the file",
		"hunk line 2 is at line 4 of 10, and 2 of 3 hunk lines match there",
		"  >     4 | \tone()",
		`the file has no line for: "\tzero()"`,
		"re-send the hunk with the lines above.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	t.Logf("message:\n%s", msg)
}

// TestHunkErrorNamesWhitespaceOnlyDifference: every line of the hunk is
// in the file, so the failure is indentation alone, and the error can
// say that instead of asking for a re-read.
func TestHunkErrorNamesWhitespaceOnlyDifference(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)
	file := "func a() {\n\tif err != nil {\n\t\treturn err\n\t}\n}\n"
	if err := ws.Write(ctx, "a.go", []byte(file)); err != nil {
		t.Fatal(err)
	}
	// Same lines, spaces instead of tabs.
	ops, err := Parse("*** Begin Patch\n*** Update File: a.go\n@@\n" +
		"     if err != nil {\n" +
		"-\t\treturn err\n" +
		"+        return nil\n" +
		"     }\n*** End Patch\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(ctx, ws, ops)
	if err == nil {
		t.Fatal("whitespace-mismatched hunk accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		"every line of the hunk is in the file from line 2",
		"with different leading or trailing space",
		"  >     2 | \tif err != nil {",
		"re-send the hunk with the file's own whitespace.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	t.Logf("message:\n%s", msg)
}

// TestHunkErrorFallsBackToAReRead: when no line of the hunk is in the
// file, there is nothing honest to quote, and the error says so
// instead of pointing at an unrelated line.
func TestHunkErrorFallsBackToAReRead(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)
	if err := ws.Write(ctx, "a.txt", nil); err != nil {
		t.Fatal(err)
	}
	ops, err := ParseAny("--- a/a.txt\n+++ b/a.txt\n@@ -9,0 +10,1 @@\n+ten\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(ctx, ws, ops)
	if err == nil {
		t.Fatal("insertion past the end accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		"the file has fewer than 9 lines",
		"re-read the file and copy the exact lines.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	t.Logf("message:\n%s", msg)
}

// TestHunkErrorNamesTheHunksOwnFirstLine: the locator settles on the
// line that agrees best, which can be any line of the hunk, so the
// number the diagnosis names has to be the hunk's first line. A window
// that opens on line 2 under a sentence that says line 3 sends the
// retry to the wrong place.
func TestHunkErrorNamesTheHunksOwnFirstLine(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)
	file := "func a() {\n" +
		"\tif err != nil {\n" +
		"\t\treturn fmt.Errorf(\"x: %w\", err)\n" +
		"\t}\n" +
		"}\n"
	if err := ws.Write(ctx, "a.go", []byte(file)); err != nil {
		t.Fatal(err)
	}
	// The same lines with spaces instead of tabs. The middle line is the
	// longest, so it is the anchor, and its line number is one past the
	// hunk's.
	ops, err := Parse("*** Begin Patch\n*** Update File: a.go\n@@\n" +
		"     if err != nil {\n" +
		"-        return fmt.Errorf(\"x: %w\", err)\n" +
		"+        return nil\n" +
		"     }\n*** End Patch\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(ctx, ws, ops)
	if err == nil {
		t.Fatal("whitespace-mismatched hunk accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		"every line of the hunk is in the file from line 2",
		"  >     2 | \tif err != nil {",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	t.Logf("message:\n%s", msg)
}

// TestHunkErrorSaysWhichFileTheNumbersCount: a later hunk is matched
// against the file as the earlier ones left it, so its line numbers can
// exceed the file on disk and a re-read would not line up with the
// quote. The error says which file the numbers count.
func TestHunkErrorSaysWhichFileTheNumbersCount(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)
	if err := ws.Write(ctx, "a.txt", []byte("one\ntwo\nthree\n")); err != nil {
		t.Fatal(err)
	}
	ops, err := Parse("*** Begin Patch\n*** Update File: a.txt\n@@\n" +
		" one\n" +
		"+one and a half\n" +
		"@@\n" +
		" two\n" +
		"-four\n" +
		"+FOUR\n*** End Patch\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(ctx, ws, ops)
	if err == nil {
		t.Fatal("stale second hunk accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		`hunk 2 in "a.txt" did not match`,
		"(line numbers are the file's after the hunks before this one " +
			"were applied)",
		// The file on disk has three lines: the fourth is hunk 1's.
		"at line 3 of 4",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	t.Logf("message:\n%s", msg)
}

// TestHunkErrorSaysWhenNothingOfTheHunkIsLeft: a region rewritten past
// recognition leaves nothing to quote, and the head does not claim a
// first context line that the file still has.
func TestHunkErrorSaysWhenNothingOfTheHunkIsLeft(t *testing.T) {
	ctx := context.Background()
	ws := memWorkspace(t)
	if err := ws.Write(ctx, "a.txt", []byte("alpha\nbeta\n")); err != nil {
		t.Fatal(err)
	}
	ops, err := Parse("*** Begin Patch\n*** Update File: a.txt\n@@\n" +
		"-gamma\n+delta\n*** End Patch\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(ctx, ws, ops)
	if err == nil {
		t.Fatal("hunk with no line in the file accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		`none of the hunk's lines is in the file any more, ` +
			`starting with "gamma"`,
		"re-read the file and copy the exact lines.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	t.Logf("message:\n%s", msg)
}
