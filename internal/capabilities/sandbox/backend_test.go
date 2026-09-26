package sandbox

import (
	"os/exec"
	goruntime "runtime"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// TestBackendTable pins the platform table every consumer reads. The
// values are the ones §4 of docs/architecture-plan.md documents; the
// table and the construction live in the same entry (backend.go), so
// changing one without the other is not expressible.
func TestBackendTable(t *testing.T) {
	cases := []struct {
		goos    string
		backend string
		probe   string
	}{
		{"darwin", "seatbelt", "sandbox-exec"},
		{"linux", "bwrap", "bwrap"},
		{"windows", "jobobject", ""},
		{"freebsd", "local", ""},
		{"", "local", ""},
	}
	for _, tc := range cases {
		if got := Backend(tc.goos); got != tc.backend {
			t.Errorf("Backend(%q) = %q, want %q", tc.goos, got, tc.backend)
		}
		if got := BackendProbe(tc.goos); got != tc.probe {
			t.Errorf("BackendProbe(%q) = %q, want %q", tc.goos, got, tc.probe)
		}
	}
}

// TestInteractiveSessions is the one value behind both the advertised
// exec_session tool and the noTTYRunner enforcement: Windows does not
// combine write confinement with ConPTY (flowcraft issue #38).
func TestInteractiveSessions(t *testing.T) {
	for goos, want := range map[string]bool{
		"darwin":  true,
		"linux":   true,
		"windows": false,
		"freebsd": true,
	} {
		if got := InteractiveSessions(goos); got != want {
			t.Errorf("InteractiveSessions(%q) = %v, want %v", goos, got, want)
		}
	}
}

// TestNewConfinedRunnerFailsClosedOnAMissingBackend checks the
// contract the child depends on: on the wrong platform (and on a host
// whose probe binary is missing) construction fails with the
// backend's own not-available error, wrapped but still classified —
// never a silent fallback to the local runner.
func TestNewConfinedRunnerFailsClosedOnAMissingBackend(t *testing.T) {
	root := t.TempDir()
	other := "windows"
	if goruntime.GOOS == "windows" {
		other = "darwin"
	}
	_, err := newConfinedRunner(other, root, nil)
	if err == nil {
		t.Fatalf("building the %s backend on %s succeeded", Backend(other), goruntime.GOOS)
	}
	if !errdefs.IsNotAvailable(err) {
		t.Fatalf("err = %v, want a not-available classification", err)
	}
}

// TestHostBackendBuildsOnThisPlatform is the executable half of the
// matrix: on a host whose backend needs an external program, the
// runner appears when the program does and not-available when it does
// not. Both outcomes are honest; a third one (local runner) is not.
func TestHostBackendBuildsOnThisPlatform(t *testing.T) {
	root := t.TempDir()
	runner, err := hostBackend(root, nil)
	probe := BackendProbe(goruntime.GOOS)
	if probe != "" {
		if _, lookErr := exec.LookPath(probe); lookErr != nil {
			if err == nil {
				t.Fatalf("backend %s accepted a missing %s",
					Backend(goruntime.GOOS), probe)
			}
			if !errdefs.IsNotAvailable(err) {
				t.Fatalf("err = %v, want a not-available classification", err)
			}
			return
		}
	}
	if err != nil {
		t.Fatalf("host backend %s: %v", Backend(goruntime.GOOS), err)
	}
	if runner == nil {
		t.Fatal("host backend returned a nil runner")
	}
	if err := runner.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
