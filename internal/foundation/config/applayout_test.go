package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppLayoutOwnsItsStateRootOnly pins the shape of an application
// layout: everything under <dataDir>/apps/<id>, a private workspace
// inside that root, and the usage key "app:<id>". The content root is
// nowhere in the layout — neither as WorkDir nor as Root — because
// nothing in the state path may write to it.
func TestAppLayoutOwnsItsStateRootOnly(t *testing.T) {
	dataDir := t.TempDir()
	layout, err := AppLayout(dataDir, "werewolf")
	if err != nil {
		t.Fatalf("AppLayout: %v", err)
	}
	root := filepath.Join(dataDir, "apps", "werewolf")
	want := map[string]string{
		"Root":          root,
		"WorkDir":       filepath.Join(root, "workspace"),
		"ID":            "app:werewolf",
		"SessionsDir":   filepath.Join(root, "sessions"),
		"SessionDBPath": filepath.Join(root, "sessions", "session.db"),
		"ApprovalsFile": filepath.Join(root, "approvals.yaml"),
		"CacheDir":      filepath.Join(root, "cache", "tools"),
		"AuditDir":      filepath.Join(root, "audit"),
		"ExportsDir":    filepath.Join(root, "exports"),
	}
	got := map[string]string{
		"Root":          layout.Root,
		"WorkDir":       layout.WorkDir,
		"ID":            layout.ID,
		"SessionsDir":   layout.SessionsDir,
		"SessionDBPath": layout.SessionDBPath,
		"ApprovalsFile": layout.ApprovalsFile,
		"CacheDir":      layout.CacheDir,
		"AuditDir":      layout.AuditDir,
		"ExportsDir":    layout.ExportsDir,
	}
	for field, wantValue := range want {
		if got[field] != wantValue {
			t.Errorf("%s = %q, want %q", field, got[field], wantValue)
		}
	}
	if layout.DataDir != dataDir {
		t.Errorf("DataDir = %q, want %q", layout.DataDir, dataDir)
	}
	// The root exists once the layout is built; the subdirectories it
	// owns are Ensure's job (what a caller that only needs the root —
	// a lock file, a diagnostic — does not pay for).
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Errorf("root not created: %v", err)
	}
	for _, dir := range []string{layout.WorkDir, layout.SessionsDir, layout.CacheDir} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s exists before Ensure (err=%v)", dir, err)
		}
	}
}

// TestAppLayoutRefusesIDsThatCannotNameADirectory pins the id rule at
// the place that joins it onto a path. Every rejected case is either a
// separator, a traversal, or a name that would collide with the
// filesystem's own vocabulary; the accepted ones are the shapes a
// manifest is allowed to declare.
func TestAppLayoutRefusesIDsThatCannotNameADirectory(t *testing.T) {
	valid := []string{
		"a",
		"hello",
		"werewolf",
		"hello.world",
		"hello_world",
		"hello-world",
		"0",
		strings.Repeat("a", 64),
	}
	for _, id := range valid {
		if !ValidAppID(id) {
			t.Errorf("ValidAppID(%q) = false, want true", id)
		}
	}
	invalid := []string{
		"",
		".",
		"..",
		".hidden",
		"-leading",
		"_leading",
		"Upper",
		"app id",
		"app/id",
		"app\\id",
		"..%2f",
		strings.Repeat("a", 65),
	}
	for _, id := range invalid {
		if ValidAppID(id) {
			t.Errorf("ValidAppID(%q) = true, want false", id)
		}
		// The layout must not build a path out of a rejected id, and
		// the error has to name the id so an import wizard can report
		// which file it came from.
		if _, err := AppLayout(t.TempDir(), id); err == nil {
			t.Errorf("AppLayout(%q) succeeded, want error", id)
		} else if !strings.Contains(err.Error(), "application id") {
			t.Errorf("AppLayout(%q) error = %v, want it to name the id", id, err)
		}
	}
	if _, err := AppLayout("  ", "hello"); err == nil {
		t.Error("AppLayout with a blank data dir succeeded, want error")
	}
}

// TestEnsureCreatesWhatTheLayoutOwns pins the one difference an
// application layout has from a workspace layout at Ensure time: it
// creates its own workspace. A workspace layout's WorkDir is the user's
// project directory, and creating it there is the bug the split exists
// to prevent.
func TestEnsureCreatesWhatTheLayoutOwns(t *testing.T) {
	dataDir := t.TempDir()

	project := filepath.Join(t.TempDir(), "project")
	workspaceLayout, err := ResolveWorkspace(dataDir, project)
	if err != nil {
		t.Fatalf("ResolveWorkspace: %v", err)
	}
	if err := workspaceLayout.Ensure(); err != nil {
		t.Fatalf("workspace Ensure: %v", err)
	}
	if _, err := os.Stat(project); !os.IsNotExist(err) {
		t.Errorf("workspace Ensure touched the project directory (err=%v)", err)
	}
	if info, err := os.Stat(workspaceLayout.SessionsDir); err != nil || !info.IsDir() {
		t.Errorf("workspace Ensure did not create the sessions dir: %v", err)
	}

	appLayout, err := AppLayout(dataDir, "hello")
	if err != nil {
		t.Fatalf("AppLayout: %v", err)
	}
	if err := appLayout.Ensure(); err != nil {
		t.Fatalf("app Ensure: %v", err)
	}
	for _, dir := range []string{
		appLayout.Root,
		appLayout.WorkDir,
		appLayout.SessionsDir,
		appLayout.CacheDir,
		appLayout.AuditDir,
		appLayout.ExportsDir,
	} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("app Ensure did not create %s: %v", dir, err)
		}
	}
}
