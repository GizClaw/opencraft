package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInWorkspace(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Symlink inside the workspace pointing outside it.
	if err := os.Symlink(secret, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
		ok   bool
	}{
		{"empty", "", false},
		{"relative file", "src/a.txt", true},
		{"absolute inside", filepath.Join(sub, "a.txt"), true},
		{"parent escape", filepath.Join(root, "..", "outside.txt"), false},
		{"symlink escape", filepath.Join(root, "escape"), false},
		{"root itself", root, true},
		{"deleted file inside", filepath.Join(root, "gone.txt"), true},
	} {
		got, err := resolveInWorkspace(root, tc.path)
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("%s: expected rejection, got %s", tc.name, got)
			}
			continue
		}
		if got != rootResolved &&
			!strings.HasPrefix(got, rootResolved+string(os.PathSeparator)) {
			t.Errorf("%s: resolved %s outside root %s", tc.name, got, rootResolved)
		}
	}
}

func TestResolveInWorkspaceRequiresWorkspace(t *testing.T) {
	if _, err := resolveInWorkspace("", "src/a.txt"); err == nil {
		t.Fatal("resolving against an empty workspace must fail")
	}
}
