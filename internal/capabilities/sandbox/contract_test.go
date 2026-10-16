package sandbox

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
)

// TestSandboxRunnerContract is the platform contract suite for the
// real OS backends: it runs on the Linux (bwrap), macOS (seatbelt) and
// Windows (job-object) CI jobs. The default local fallback is skipped.
func TestSandboxRunnerContract(t *testing.T) {
	switch goruntime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("no OS-level sandbox backend on " + goruntime.GOOS)
	}
	if os.Getenv("OPENCRAFT_SKIP_SANDBOX_CONTRACT") != "" {
		t.Skip("sandbox contract disabled")
	}
	ctx := context.Background()
	workDir := t.TempDir()
	runner, policy, err := SandboxRunner(ctx, workDir, SandboxPolicy{})
	if err != nil {
		t.Fatalf("SandboxRunner: %v", err)
	}
	defer func() { _ = runner.Close() }()
	confined := coresandbox.WithDefaults(runner, coresandbox.ExecOptions{
		Env: policy,
	})
	shell := "/bin/sh"
	shellArgs := func(script string) []string { return []string{"-c", script} }
	secretProbe := `printf %s "$OPENCRAFT_CONTRACT_SECRET"`
	if goruntime.GOOS == "windows" {
		shell = "cmd"
		shellArgs = func(script string) []string { return []string{"/c", script} }
		secretProbe = "echo %OPENCRAFT_CONTRACT_SECRET%"
	}

	t.Run("exec works inside the root", func(t *testing.T) {
		res, err := coresandbox.Exec(ctx, confined, shell,
			shellArgs("echo contract-ok"), coresandbox.ExecOptions{})
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		if !strings.Contains(res.Stdout, "contract-ok") {
			t.Fatalf("stdout = %q", res.Stdout)
		}
	})

	t.Run("writes outside the root are denied", func(t *testing.T) {
		outside := outsideWritePath(workDir)
		res, err := coresandbox.Exec(ctx, confined, shell,
			shellArgs("echo pwn > "+outside), coresandbox.ExecOptions{
				WorkDir: workDir,
			})
		if _, statErr := os.Stat(outside); statErr == nil {
			t.Fatalf("outside write escaped the sandbox (exec err=%v)", err)
		}
		if err != nil {
			return
		}
		if res.ExitCode == 0 {
			// The probe path was writable inside the sandbox, so this
			// run says nothing about denial handling: the host check
			// above passed for the wrong reason. Fail loudly rather
			// than leaving a vacuous contract.
			t.Fatalf("probe path %q was writable inside the sandbox", outside)
		}
		// The escalation prompt keys off this predicate, so a real
		// backend refusal must be recognisable from the finished
		// command's own output.
		if reason, denied := coresandbox.Denied(res); !denied {
			t.Fatalf("real refusal not detected: exit=%d stderr=%q",
				res.ExitCode, res.Stderr)
		} else if reason == "" {
			t.Fatal("denial without a reason")
		}
	})

	t.Run("parent secrets are not injected", func(t *testing.T) {
		t.Setenv("OPENCRAFT_CONTRACT_SECRET", "s3cr3t-value")
		res, err := coresandbox.Exec(ctx, confined, shell,
			shellArgs(secretProbe),
			coresandbox.ExecOptions{WorkDir: workDir})
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		if strings.Contains(res.Stdout, "s3cr3t-value") {
			t.Fatalf("parent secret leaked into the sandbox: %q", res.Stdout)
		}
	})

	// The staged copy of an activated skill is read and run by confined
	// commands but must not be rewritable by them: it lives outside
	// every write grant (the default config points staging at
	// APP_HOME/staged, deliberately not at the sandbox-writable cache),
	// because the model reads it as skill reference material. The probe
	// dir stands in for that location: the same "outside the workspace,
	// outside every grant" shape, not the real APP_HOME path (whose
	// mount semantics differ per host).
	t.Run("staged scripts run but cannot be rewritten", func(t *testing.T) {
		staged := stagedProbeDir(t, workDir)
		if goruntime.GOOS == "windows" {
			// No shebang to run: the positive control reads the file,
			// which is what a staged reference file has to allow. Every
			// lane needs one, or the write probe below would also pass
			// on a sandbox whose shell never started.
			readme := filepath.Join(staged, "probe.txt")
			if err := os.WriteFile(readme, []byte("staged-ok"), 0o644); err != nil {
				t.Fatal(err)
			}
			// The argv this harness hands to cmd.exe is escaped for the
			// C runtime, not for cmd, so a quoted path arrives mangled
			// ("the filename ... syntax is incorrect"); the read has to
			// spell the path bare, which cmd accepts only without
			// spaces. CI's temp path has none; a machine whose temp
			// path does keeps the write probe below and loses only the
			// read half.
			if strings.ContainsAny(readme, " \t") {
				t.Logf("staged read skipped: cmd cannot take a quoted path %q", readme)
			} else {
				res, err := coresandbox.Exec(ctx, confined, shell,
					[]string{"/c", "type " + readme},
					coresandbox.ExecOptions{WorkDir: workDir})
				if err != nil {
					t.Fatalf("read staged file: %v", err)
				}
				if !strings.Contains(res.Stdout, "staged-ok") {
					t.Fatalf("staged read exit=%d stdout=%q stderr=%q",
						res.ExitCode, res.Stdout, res.Stderr)
				}
			}
		} else {
			script := filepath.Join(staged, "run.sh")
			if err := os.WriteFile(script,
				[]byte("#!/bin/sh\necho staged-ok\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			// Running the file itself is the claim: it needs the read
			// bit, the executable bit staging preserves, and a mount
			// that permits execution.
			res, err := coresandbox.Exec(ctx, confined, script, nil,
				coresandbox.ExecOptions{WorkDir: workDir})
			if err != nil {
				t.Fatalf("exec staged script: %v", err)
			}
			if res.ExitCode != 0 || !strings.Contains(res.Stdout, "staged-ok") {
				t.Fatalf("staged script exit=%d stdout=%q stderr=%q",
					res.ExitCode, res.Stdout, res.Stderr)
			}
		}
		probe := filepath.Join(staged, "rewritten.txt")
		res, err := coresandbox.Exec(ctx, confined, shell,
			shellArgs("echo pwn > "+probe), coresandbox.ExecOptions{
				WorkDir: workDir,
			})
		if _, statErr := os.Stat(probe); statErr == nil {
			t.Fatalf("staged copy was rewritten from inside the sandbox (exec err=%v)", err)
		}
		if err == nil && res.ExitCode == 0 {
			t.Fatal("write into the staged area reported success")
		}
	})
}

// outsideWritePath returns a path the confined backend must refuse.
//
// Linux mounts a private writable tmpfs at /tmp (core/sandbox/bwrap
// flags.go), and the test workspace lives under it, so its siblings are
// writable *inside* the sandbox: the write succeeds into the sandbox's
// own tmpfs and the host simply never sees the file. /var/tmp sits on
// the read-only root bind instead, which is the refusal this test is
// about. macOS and Windows keep the workspace sibling, where seatbelt
// and the low-integrity token reject the write directly.
func outsideWritePath(workDir string) string {
	name := "opencraft-contract-outside-" + goruntime.GOOS +
		"-" + strconv.Itoa(os.Getpid()) + ".txt"
	if goruntime.GOOS == "linux" {
		return filepath.Join("/var/tmp", name)
	}
	return filepath.Join(filepath.Dir(workDir), name)
}

// stagedProbeDir stands in for the staged-skill area: a host directory
// outside the workspace and outside every write grant, the way the
// default config places APP_HOME/staged. Linux binds only the workspace
// into its private /tmp tmpfs, so the probe has to live on the
// read-only root bind to be visible inside at all; macOS and Windows
// keep host paths visible, where a sibling of the workspace is already
// outside the grants.
func stagedProbeDir(t *testing.T, workDir string) string {
	t.Helper()
	parent := filepath.Dir(workDir)
	if goruntime.GOOS == "linux" {
		parent = "/var/tmp"
	}
	dir, err := os.MkdirTemp(parent, "opencraft-staged-")
	if err != nil {
		t.Fatalf("staged probe dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
