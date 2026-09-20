package patch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyToDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: x\ndescription: d\n---\n\n# X\nOld line.\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "scripts", "run.py"),
		[]byte("print('v1')\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}

	results, err := ApplyToDir(dir, `*** Begin Patch
*** Update File: SKILL.md
@@
-Old line.
+New line.
*** Update File: scripts/run.py
@@
-print('v1')
+print('v2')
*** Add File: references/notes.md
+# Notes
+Keep them short.
*** End Patch
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "New line.") {
		t.Fatalf("SKILL.md = %q, %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(dir, "scripts", "run.py"))
	if err != nil || !strings.Contains(string(data), "v2") {
		t.Fatalf("run.py = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "references", "notes.md")); err != nil {
		t.Fatalf("added file missing: %v", err)
	}

	// Hunk mismatch is an error.
	if _, err := ApplyToDir(dir, `*** Begin Patch
*** Update File: SKILL.md
@@
-Does not exist.
+Still not.
*** End Patch
`); err == nil {
		t.Fatal("hunk mismatch must fail")
	}

	// Escaping paths are rejected.
	if _, err := ApplyToDir(dir, `*** Begin Patch
*** Update File: ../outside.txt
@@
-x
+y
*** End Patch
`); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escape error = %v", err)
	}
}

// TestApplyToDirContextLinesWithoutSpaceMarker: the marker-less context
// line must also *apply*. Matching still happens against the real file, so
// accepting it cannot patch the wrong place: a line that is not there
// fails the hunk instead.
func TestApplyToDirContextLinesWithoutSpaceMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.go")
	if err := os.WriteFile(path, []byte(
		"func stats() {\n"+
			"\t).Scan(&out.Profile); err != nil {\n"+
			"\treturn out, nil\n"+
			"}\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyToDir(dir, `*** Begin Patch
*** Update File: stats.go
@@
-	).Scan(&out.Profile); err != nil {
+	var profileChars int
+	).Scan(&out.Profile, &profileChars); err != nil {
+	out.Chars += profileChars
 	return out, nil
 }
*** End Patch
`); err != nil {
		t.Fatalf("ApplyToDir: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"var profileChars int",
		").Scan(&out.Profile, &profileChars); err != nil {",
		"out.Chars += profileChars",
		"\treturn out, nil",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("patched file missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, ").Scan(&out.Profile); err != nil {") {
		t.Errorf("removed line still present:\n%s", got)
	}
}

func TestApplyToDirSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(outside, "secret.txt"), []byte("s"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyToDir(dir, `*** Begin Patch
*** Update File: linked/secret.txt
@@
-s
+x
*** End Patch
`); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("symlink escape error = %v", err)
	}
}
