package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/utils/envpath"
)

// TestNewResolvesProcessPath covers the launch the module exists for: a
// Finder/Dock start hands the process launchd's minimal PATH, and New has
// to merge the persisted override and the platform candidates into it
// before anything spawns.
func TestNewResolvesProcessPath(t *testing.T) {
	userDir := t.TempDir()
	prepend := t.TempDir()
	document := `{"path":{"prepend":["` + prepend + `"]}}`
	if err := os.WriteFile(
		filepath.Join(userDir, "desktop.json"), []byte(document), 0o600,
	); err != nil {
		t.Fatalf("write prefs: %v", err)
	}
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	d, err := New(Options{UserDir: userDir, DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("desktop.New: %v", err)
	}
	defer d.Shutdown(context.Background())

	got := os.Getenv("PATH")
	if !strings.HasPrefix(got, prepend) {
		t.Fatalf("process PATH = %q, want the configured override first", got)
	}
	for _, dir := range []string{"/usr/bin", "/bin"} {
		if !strings.Contains(got, dir) {
			t.Fatalf("process PATH = %q, want %s kept", got, dir)
		}
	}
	report, ok := d.core.PathReport()
	if !ok {
		t.Fatal("no PATH report recorded by desktop.New")
	}
	if report.Path != got {
		t.Fatalf("reported PATH = %q, want the process PATH %q", report.Path, got)
	}
	if dirs := report.Dirs(envpath.SourcePrepend); len(dirs) != 1 ||
		dirs[0] != prepend {
		t.Fatalf("prepend segments = %v, want [%s]", dirs, prepend)
	}
	if dirs := report.Dirs(envpath.SourceInherited); len(dirs) == 0 {
		t.Fatal("no inherited segments recorded")
	}
}
