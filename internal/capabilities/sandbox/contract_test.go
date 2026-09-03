package sandbox

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
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
		outside := filepath.Join(filepath.Dir(workDir), "contract-outside-"+goruntime.GOOS+".txt")
		_, err := coresandbox.Exec(ctx, confined, shell,
			shellArgs("echo pwn > "+outside), coresandbox.ExecOptions{
				WorkDir: workDir,
			})
		if _, statErr := os.Stat(outside); statErr == nil {
			t.Fatalf("outside write escaped the sandbox (exec err=%v)", err)
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
}
