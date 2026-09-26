// Package platform_test carries the executable half of the platform
// capability matrix (docs/architecture-plan.md §4): each platform's
// confined backend, whether interactive sessions exist, the
// single-instance endpoint mechanism, and how a file is marked hidden.
//
// The values that are computed from a goos parameter — backend name,
// probe program, interactive sessions, default shell — are asserted
// for every platform on any host, so a Linux run still pins the
// Windows row. The columns whose implementation is picked by build
// tags (endpoint, hidden marking, the process lock) are asserted for
// the host the test runs on: the table names the implementation each
// row pins, and the assertion runs on that platform's lane (W6), so a
// change to the implementation without the table turns red there.
//
// The one deliberate hole: Windows does not serve interactive sessions
// (flowcraft issue #38 — the job-object backend does not combine write
// confinement with ConPTY). That gap is a value here
// (sandbox.InteractiveSessions), not an absence of code, so "we do not
// offer exec_session on Windows" is a testable statement.
package platform_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	ocsandbox "github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/foundation/platform/fshidden"
	"github.com/GizClaw/opencraft/internal/foundation/platform/guilock"
	"github.com/GizClaw/opencraft/internal/foundation/platform/shelldetect"
	"github.com/GizClaw/opencraft/internal/foundation/platform/wslock"
)

// row is one platform's line of the matrix. The comment on each field
// names the implementation it pins; change the implementation and this
// table (or the host-row assertions below) has to change with it.
type row struct {
	backend string // sandbox.Backend / backend.go
	probe   string // sandbox.BackendProbe: external binary, "" = built in
	tty     bool   // sandbox.InteractiveSessions: exec_session offered?
	shell   string // shelldetect.Default(goos).Program
	// endpoint is the single-instance raise channel (guilock): a unix
	// socket under the state root, or a Windows named pipe.
	endpoint string
	// hidden is how this platform marks a hidden directory entry
	// (fshidden): the leading-dot convention, or the Windows attributes
	// Explorer honours.
	hidden string
	// execd is the private parent/child channel (execd): socketpair on
	// unix, a named pipe with an SDDL on Windows.
	execd string
}

func matrix() map[string]row {
	return map[string]row{
		"darwin": {
			backend:  "seatbelt",
			probe:    "sandbox-exec",
			tty:      true,
			shell:    "/bin/sh",
			endpoint: "unix socket <state root>/gui.sock",
			hidden:   "leading dot",
			execd:    "socketpair",
		},
		"linux": {
			backend:  "bwrap",
			probe:    "bwrap",
			tty:      true,
			shell:    "/bin/sh",
			endpoint: "unix socket <state root>/gui.sock",
			hidden:   "leading dot",
			execd:    "socketpair",
		},
		"windows": {
			backend:  "jobobject",
			probe:    "",
			tty:      false, // issue #38
			shell:    "cmd.exe",
			endpoint: `named pipe \\.\pipe\opencraft-gui-<identity>`,
			hidden:   "leading dot or FILE_ATTRIBUTE_HIDDEN|SYSTEM",
			execd:    "named pipe with SDDL",
		},
	}
}

// TestPlatformCapabilityMatrixValues asserts the whole table on every
// host: these four columns come from goos-parameterised functions, so
// a macOS or Linux CI lane still pins the Windows answer.
func TestPlatformCapabilityMatrixValues(t *testing.T) {
	for goos, want := range matrix() {
		t.Run(goos, func(t *testing.T) {
			if got := ocsandbox.Backend(goos); got != want.backend {
				t.Errorf("Backend(%q) = %q, want %q", goos, got, want.backend)
			}
			if got := ocsandbox.BackendProbe(goos); got != want.probe {
				t.Errorf("BackendProbe(%q) = %q, want %q", goos, got, want.probe)
			}
			if got := ocsandbox.InteractiveSessions(goos); got != want.tty {
				t.Errorf("InteractiveSessions(%q) = %v, want %v", goos, got, want.tty)
			}
			if got := shelldetect.Default(goos).Program; got != want.shell {
				t.Errorf("shelldetect.Default(%q) = %q, want %q", goos, got, want.shell)
			}
		})
	}
}

// TestHostRowMatchesCompiledImplementation asserts the rows whose
// implementation is chosen by build tags against the host's own
// packages: dot-prefixed entries are hidden everywhere (the Windows
// attribute branch is covered by fshidden's own windows test).
func TestHostRowMatchesCompiledImplementation(t *testing.T) {
	host := hostRow(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".probe"))
	writeFile(t, filepath.Join(dir, "probe.txt"))
	entries := entriesByName(t, dir)
	if !fshidden.Hidden(entries[".probe"]) {
		t.Errorf("%s: a dot-prefixed entry must be hidden (%s)", runtime.GOOS, host.hidden)
	}
	if fshidden.Hidden(entries["probe.txt"]) {
		t.Errorf("%s: a plain entry must stay visible (%s)", runtime.GOOS, host.hidden)
	}
}

// TestHostEndpointMechanism exercises the single-instance channel the
// host row names: acquire the GUI lock, serve the raise endpoint and
// round-trip a raise through it. The unix row additionally asserts the
// documented location (a socket file in the state root); the Windows
// row asserts the opposite — no file, because the endpoint lives in
// the kernel's pipe namespace — and that the raise still arrives.
func TestHostEndpointMechanism(t *testing.T) {
	host := hostRow(t)
	root := shortRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	lock, err := guilock.Acquire(ctx, root, "")
	if err != nil {
		t.Fatalf("guilock.Acquire: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	if !lock.Owned() {
		t.Fatal("a fresh state root must be owned by this process")
	}
	raised := make(chan guilock.Launch, 1)
	lock.Serve(ctx, func(l guilock.Launch) { raised <- l })

	if err := guilock.RequestRaise(ctx, root, "", guilock.Launch{}); err != nil {
		t.Fatalf("%s: raise did not arrive (%s): %v", runtime.GOOS, host.endpoint, err)
	}
	select {
	case <-raised:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: raise handler was not called (%s)", runtime.GOOS, host.endpoint)
	}

	socket := filepath.Join(root, "gui.sock")
	_, statErr := os.Stat(socket)
	switch runtime.GOOS {
	case "windows":
		if statErr == nil {
			t.Errorf("the windows endpoint must be a named pipe, found %s", socket)
		}
	default:
		if statErr != nil {
			t.Errorf("%s endpoint %s: %v", runtime.GOOS, socket, statErr)
		}
	}
}

// TestHostProcessLock exercises the process lock the host row uses
// (flock on unix, LockFileEx on Windows) through its portable API: the
// lock file appears, the holder records itself, and releasing hands
// the root over. Contention across processes is covered by wslock's
// own tests.
func TestHostProcessLock(t *testing.T) {
	host := hostRow(t)
	root := t.TempDir()
	path := filepath.Join(root, wslock.FileName)
	ctx := context.Background()
	handle, err := wslock.Acquire(ctx, path, guilock.Kind)
	if err != nil {
		t.Fatalf("wslock.Acquire: %v", err)
	}
	if !handle.Owned() {
		t.Fatal("a fresh lock must be owned by this process")
	}
	if info := handle.Info(); info.PID != os.Getpid() {
		t.Errorf("holder pid = %d, want %d", info.PID, os.Getpid())
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("lock file %s (%s): %v", path, host.execd, err)
	}
	if err := handle.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func hostRow(t *testing.T) row {
	t.Helper()
	r, ok := matrix()[runtime.GOOS]
	if !ok {
		t.Skipf("no matrix row for %s", runtime.GOOS)
	}
	return r
}

// shortRoot returns a state root short enough for a unix socket path:
// the macOS per-test temp directory already exceeds guilock's socket
// budget, and guilock would (correctly) fall back to TMPDIR, hiding
// the documented location this test asserts.
func shortRoot(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return t.TempDir()
	}
	root := filepath.Join("/tmp", fmt.Sprintf("oc-platform-matrix-%d", os.Getpid()))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Skipf("cannot create %s: %v", root, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func entriesByName(t *testing.T, dir string) map[string]os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]os.DirEntry, len(entries))
	for _, e := range entries {
		out[e.Name()] = e
	}
	return out
}
