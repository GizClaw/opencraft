// Package kraftfixture builds the plugin kraft fixtures the desktop
// tests drive over the real subprocess protocol. The fixtures live here
// rather than in one adapter's testdata because more than one adapter
// needs the same program: the core tests (secrets, telemetry, the
// lifecycle of a mutating plugin) and the bindings tests (what each
// mutation does to a running process) both start one, and a second copy
// of the same Go program is a copy that drifts.
package kraftfixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Fixture names, one per program under testdata/.
const (
	// SecretPlugin answers secret.probe with what the host's keyring
	// said, and secret.set/get round-trips a token through the host.
	SecretPlugin = "secretplugin"
	// TelemetryPlugin calls telemetry.configure with its own OTLP
	// endpoint and then stays up.
	TelemetryPlugin = "telemetryplugin"
	// PIDMethod is the method every fixture answers with its own
	// process id: it is how a test tells one process from the next
	// after a mutation stopped the one before it.
	PIDMethod = "fixture.pid"
)

// Build compiles one fixture into this test's temp directory and returns
// the binary path. Each call builds: a fixture is a few hundred lines and
// a test that starts it is already the slow part.
func Build(t testing.TB, name string) string {
	t.Helper()
	dir := fixtureDir(t, name)
	out := filepath.Join(t.TempDir(), name)
	build := exec.Command("go", "build", "-o", out, dir)
	if outb, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s fixture: %v\n%s", name, err, outb)
	}
	return out
}

// fixtureDir resolves testdata/<name> next to this package, so the build
// does not depend on the caller's working directory.
func fixtureDir(t testing.TB, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve fixture dir for %s", name)
	}
	dir := filepath.Join(filepath.Dir(file), "testdata", name)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture %s is missing: %v", name, err)
	}
	return dir
}
