package patch

import (
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
