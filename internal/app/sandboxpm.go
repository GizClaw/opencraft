package app

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/bwrap"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	"github.com/GizClaw/flowcraft/core/sandbox/seatbelt"

	"github.com/GizClaw/opencraft/internal/config"
)

// SandboxRunner builds the platform sandbox runner for the execd child
// (seatbelt on macOS, bwrap on Linux, local elsewhere) plus the default
// environment policy that points Go build/tmp caches into
// ~/.opencraft/cache.
func SandboxRunner(
	_ context.Context,
	workDir string,
) (sandbox.Runner, sandbox.EnvPolicy, error) {
	dataDir, err := config.UserDataDir()
	if err != nil {
		return nil, sandbox.EnvPolicy{}, err
	}
	cacheDir := filepath.Join(dataDir, "cache")
	for _, sub := range []string{"go", "tmp"} {
		if err := os.MkdirAll(filepath.Join(cacheDir, sub), 0o755); err != nil {
			return nil, sandbox.EnvPolicy{}, err
		}
	}
	policy := sandbox.EnvPolicy{
		Allow: []string{"PATH", "HOME"},
		Inject: map[string]string{
			"GOCACHE": filepath.Join(cacheDir, "go"),
			"TMPDIR":  filepath.Join(cacheDir, "tmp"),
		},
	}
	switch goruntime.GOOS {
	case "darwin":
		runner, err := seatbelt.New(workDir, seatbelt.WithWritablePaths(cacheDir))
		if err == nil {
			return runner, policy, nil
		}
		return sandboxlocal.New(workDir), sandbox.EnvPolicy{}, nil
	case "linux":
		runner, err := bwrap.New(workDir, bwrap.WithWritablePaths(cacheDir))
		if err == nil {
			return runner, policy, nil
		}
		return sandboxlocal.New(workDir), sandbox.EnvPolicy{}, nil
	default:
		return sandboxlocal.New(workDir), sandbox.EnvPolicy{}, nil
	}
}
