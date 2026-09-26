package sandbox

import (
	"fmt"
	goruntime "runtime"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/bwrap"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	"github.com/GizClaw/flowcraft/core/sandbox/seatbelt"
	sbwindows "github.com/GizClaw/flowcraft/core/sandbox/windows"
)

// This file is the one place that answers "which OS isolation layer
// does confined exec use here, what does it need, and can it serve
// TTY sessions". It used to be answered in three places — the
// parent-side runner in hostsandbox.go, the execd child in
// sandboxpm.go, and the settings/diagnostics page — and the third
// answer was wrong (it reported "local" for Windows, where the child
// actually runs the job-object backend).
//
// The name and the construction live in the same table entry, so a
// platform whose construction changes cannot keep the old name.

// backend is one row of the platform table.
type backend struct {
	// name is the display name of the isolation layer. It follows the
	// mechanism (jobobject), not the flowcraft registry spelling: the
	// same backend is registered as impl "windows"
	// (sbwindows.BackendName) because that is how a deployment selects
	// it by platform.
	name string
	// probe is the external program the backend needs on PATH. Empty
	// means the backend is built into the OS layer and needs nothing
	// installed.
	probe string
	// build constructs the runner over root with the extra writable
	// paths. It returns the backend's own error verbatim so callers
	// keep errdefs classification (NotAvailable on the wrong
	// platform, validation on bad settings).
	build func(root string, writable []string) (coresandbox.Runner, error)
	// tty is whether the backend serves interactive (TTY) sessions on
	// this platform. Windows says no: flowcraft's job-object backend
	// advertises TTY and then rejects the start, because write
	// confinement and ConPTY are not combined there (flowcraft issue
	// #38). The row is the single answer — InteractiveSessions reads
	// it, and withPlatformCapabilities makes the runner it hands out
	// agree with it.
	tty bool
}

// backendFor returns the platform row for goos. Anything that is not
// darwin/linux/windows gets the local (unsandboxed) runner, which is
// the historical fallback and what the capability matrix documents.
func backendFor(goos string) backend {
	switch goos {
	case "darwin":
		return backend{
			name:  "seatbelt",
			probe: "sandbox-exec",
			tty:   true,
			build: func(root string, writable []string) (coresandbox.Runner, error) {
				return seatbelt.New(root, seatbelt.WithWritablePaths(writable...))
			},
		}
	case "linux":
		return backend{
			name:  "bwrap",
			probe: "bwrap",
			tty:   true,
			build: func(root string, writable []string) (coresandbox.Runner, error) {
				return bwrap.New(root, bwrap.WithWritablePaths(writable...))
			},
		}
	case "windows":
		return backend{
			name: "jobobject",
			// Built into the OS layer: a restricted token plus a job
			// object, nothing to install and nothing to probe.
			probe: "",
			tty:   false, // issue #38
			build: func(root string, writable []string) (coresandbox.Runner, error) {
				// WithWriteConfinement is the difference from the
				// unconfined (YOLO) path, which uses the same backend
				// without it (see UnconfinedRunner).
				return sbwindows.New(root,
					sbwindows.WithWriteConfinement(),
					sbwindows.WithWritablePaths(writable...))
			},
		}
	default:
		return backend{
			name:  "local",
			probe: "",
			tty:   true,
			build: func(root string, _ []string) (coresandbox.Runner, error) {
				return sandboxlocal.New(root), nil
			},
		}
	}
}

// Backend names the OS isolation layer that confined exec runs under
// on goos: "seatbelt" | "bwrap" | "jobobject" | "local". The
// parent-side runner, the execd child and the diagnostics page all
// read it, so a deployment's runner and the page describing it cannot
// disagree.
func Backend(goos string) string { return backendFor(goos).name }

// BackendProbe returns the external program the confined backend needs
// on PATH, or "" when the backend is self-contained (Windows, local).
// Availability reporting uses it: a missing probe is the one reason a
// backend can be unavailable while being the platform's answer.
func BackendProbe(goos string) string { return backendFor(goos).probe }

// InteractiveSessions reports whether the confined backend serves TTY
// sessions on goos. It is the table's tty column, so the answer the
// diagnostics page prints and the runner that validates a TTY start
// cannot disagree. Windows cannot today: the job-object backend does
// not combine write confinement with ConPTY (flowcraft issue #38), so
// opencraft neither advertises the session tool nor accepts a TTY
// start there. Both readers — the exec tool list and noTTYRunner — go
// through this value.
func InteractiveSessions(goos string) bool { return backendFor(goos).tty }

// newConfinedRunner builds the confined backend for goos over root.
// The parent-side factory (HostSandboxFactory) and the execd child
// (SandboxRunnerWithCache) both construct their confined runner here,
// so a platform is described once.
func newConfinedRunner(
	goos, root string, writable []string,
) (coresandbox.Runner, error) {
	spec := backendFor(goos)
	runner, err := spec.build(root, writable)
	if err != nil {
		return nil, fmt.Errorf("opencraft sandbox: %s: %w", spec.name, err)
	}
	return withPlatformCapabilities(goos, runner), nil
}

// withPlatformCapabilities decorates runner so what it advertises
// matches its platform row: a row without TTY support must not hand out
// a runner that says it has it. Every construction path goes through it
// — the confined runner above and the unconfined (YOLO) runner in
// sandboxpm.go — so "no sessions on this platform" cannot be true in
// one path and not the other.
func withPlatformCapabilities(goos string, runner coresandbox.Runner) coresandbox.Runner {
	if backendFor(goos).tty {
		return runner
	}
	return noTTYRunner{runner}
}

// hostBackend builds the confined runner for the platform this binary
// is running on.
func hostBackend(root string, writable []string) (coresandbox.Runner, error) {
	return newConfinedRunner(goruntime.GOOS, root, writable)
}
