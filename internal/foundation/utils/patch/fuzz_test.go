package patch

import "testing"

func FuzzParsePatch(f *testing.F) {
	for _, seed := range []string{
		"*** Begin Patch\n*** Add File: a.txt\n+x\n*** End Patch\n",
		"*** Begin Patch\n*** Update File: a.txt\n@@\n-x\n+y\n*** End Patch\n",
		"garbage",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		ops, err := Parse(input)
		if err != nil {
			return
		}
		// A parseable patch must also render without panicking.
		_, _ = Diff(input, nil)
		for _, op := range ops {
			_ = op
		}
	})
}

// FuzzParseAny covers the unified-diff half, which FuzzParsePatch never
// reaches: Parse only reads the codex envelope, while ParseAny (the
// entry the tool actually uses) branches on LooksLikeUnifiedDiff and
// parses `git diff` output through a different reader. A patch that
// survives one of the two formats says nothing about the other, and the
// unified reader is the one carrying header arithmetic (line counts,
// insertion positions) that a malformed diff can drive negative.
func FuzzParseAny(f *testing.F) {
	for _, seed := range []string{
		"diff --git a/a.txt b/a.txt\nindex 111..222 100644\n--- a/a.txt\n+++ b/a.txt\n@@ -1,2 +1,2 @@\n-old\n+new\n context\n",
		"--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-x\n+y\n",
		"--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1,2 @@\n+one\n+two\n",
		"--- a/gone.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-one\n-two\n",
		// Headers a hostile or broken producer can write: a huge count, a
		// zero-length hunk, a reversed range, a missing newline at EOF.
		"--- a/a.txt\n+++ b/a.txt\n@@ -99999999999999,99999999999999 +1,1 @@\n-x\n+y\n",
		"--- a/a.txt\n+++ b/a.txt\n@@ -0,0 +0,0 @@\n",
		"--- a/a.txt\n+++ b/a.txt\n@@ -5,3 +2,9 @@\n-a\n+b\n",
		"--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-x\n+y",
		"diff --git a/a b/a\n@@\n\\ No newline at end of file\n",
		"garbage",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		ops, err := ParseAny(input)
		if err != nil {
			return
		}
		// Rendering a parseable patch must not panic either, and the
		// render path re-parses the text on its own.
		_, _ = Diff(input, nil)
		for _, op := range ops {
			if op == nil {
				t.Fatal("ParseAny returned a nil op")
			}
			// Every op names a path and either carries content or hunks:
			// an op that does neither would render an empty change.
			if op.path == "" {
				t.Fatalf("op %v has no path", op.kind)
			}
		}
	})
}
