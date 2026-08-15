package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/bwrap"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	"github.com/GizClaw/flowcraft/core/sandbox/seatbelt"

	"github.com/GizClaw/opencraft/internal/config"
)

// EnvPolicyConfig is the configurable environment policy: allow lists
// which host variables the sandboxed child can observe (a full
// replacement list), inject sets or overrides values. It mirrors
// flowcraft's sandbox.EnvPolicy with config-friendly JSON names.
type EnvPolicyConfig struct {
	Allow  []string          `json:"allow,omitempty"`
	Inject map[string]string `json:"inject,omitempty"`
}

// SandboxPolicy is the sandbox policy handed from the parent process
// to the execd child: project-configured writable paths plus the
// environment policy. It is serialized over the -sandbox-policy flag;
// a nil EnvPolicy leaves the environment policy empty (no allow
// filter, no injection), so spawned commands inherit the host
// environment unless the deploy document configures env_policy.
type SandboxPolicy struct {
	WritablePaths []string         `json:"writable_paths,omitempty"`
	EnvPolicy     *EnvPolicyConfig `json:"env_policy,omitempty"`
}

// SandboxRunner builds the platform sandbox runner for the execd child
// (seatbelt on macOS, bwrap on Linux, local elsewhere). Writable paths
// are the internal cache directory plus the project-configured
// writable_paths; the environment policy is the configured policy
// verbatim, or empty when the deploy document does not declare one.
// Sandbox construction failures are fatal: silently falling back to the
// local runner would let the parent believe commands are isolated when
// they are not, so the child fails closed instead.
func SandboxRunner(
	_ context.Context,
	workDir string,
	pol SandboxPolicy,
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
	var policy sandbox.EnvPolicy
	if pol.EnvPolicy != nil {
		policy = sandbox.EnvPolicy{
			Allow:  pol.EnvPolicy.Allow,
			Inject: pol.EnvPolicy.Inject,
		}
	}
	writable := append([]string{cacheDir}, pol.WritablePaths...)
	writable = dedupeStrings(writable)
	switch goruntime.GOOS {
	case "darwin":
		runner, err := seatbelt.New(workDir, seatbelt.WithWritablePaths(writable...))
		if err != nil {
			return nil, sandbox.EnvPolicy{}, fmt.Errorf(
				"opencraft sandbox: seatbelt: %w", err)
		}
		return runner, policy, nil
	case "linux":
		runner, err := bwrap.New(workDir, bwrap.WithWritablePaths(writable...))
		if err != nil {
			return nil, sandbox.EnvPolicy{}, fmt.Errorf(
				"opencraft sandbox: bwrap: %w", err)
		}
		return runner, policy, nil
	default:
		return sandboxlocal.New(workDir), policy, nil
	}
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
