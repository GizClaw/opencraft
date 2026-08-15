package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/bwrap"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	"github.com/GizClaw/flowcraft/core/sandbox/seatbelt"

	"github.com/GizClaw/opencraft/internal/config"
)

// EnvPolicyConfig is the configurable environment policy: allow lists
// which host variables the sandboxed child can observe (a full
// replacement list), inject sets or overrides values.
type EnvPolicyConfig struct {
	Allow  []string          `json:"allow,omitempty"`
	Inject map[string]string `json:"inject,omitempty"`
}

// SandboxPolicy is the sandbox policy handed from the parent process
// to the execd child: project-configured writable paths plus the
// environment policy. It is serialized over the -sandbox-policy flag.
type SandboxPolicy struct {
	WritablePaths []string         `json:"writable_paths,omitempty"`
	EnvPolicy     *EnvPolicyConfig `json:"env_policy,omitempty"`
}

// SandboxPolicy converts HostSandboxSettings into the policy handed to
// the execd child.
func (s HostSandboxSettings) SandboxPolicy() SandboxPolicy {
	pol := SandboxPolicy{WritablePaths: s.WritablePaths}
	if s.EnvPolicy != nil {
		pol.EnvPolicy = &EnvPolicyConfig{
			Allow:  s.EnvPolicy.Allow,
			Inject: s.EnvPolicy.Inject,
		}
	}
	return pol
}

// SandboxRunner builds the platform sandbox runner for the execd child
// (seatbelt on macOS, bwrap on Linux, local elsewhere). Writable paths
// are the internal cache directory plus the project-configured
// writable_paths; the environment policy is the configured policy
// verbatim. Sandbox construction failures are fatal: silently falling
// back to the local runner would let the parent believe commands are
// isolated when they are not, so the child fails closed instead.
func SandboxRunner(
	_ context.Context,
	workDir string,
	pol SandboxPolicy,
) (coresandbox.Runner, coresandbox.EnvPolicy, error) {
	dataDir, err := config.UserDataDir()
	if err != nil {
		return nil, coresandbox.EnvPolicy{}, err
	}
	cacheDir := filepath.Join(dataDir, "cache")
	for _, sub := range []string{"go", "tmp"} {
		if err := os.MkdirAll(filepath.Join(cacheDir, sub), 0o755); err != nil {
			return nil, coresandbox.EnvPolicy{}, err
		}
	}
	var policy coresandbox.EnvPolicy
	if pol.EnvPolicy != nil {
		policy = coresandbox.EnvPolicy{
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
			return nil, coresandbox.EnvPolicy{}, fmt.Errorf(
				"opencraft sandbox: seatbelt: %w", err)
		}
		return runner, policy, nil
	case "linux":
		runner, err := bwrap.New(workDir, bwrap.WithWritablePaths(writable...))
		if err != nil {
			return nil, coresandbox.EnvPolicy{}, fmt.Errorf(
				"opencraft sandbox: bwrap: %w", err)
		}
		return runner, policy, nil
	default:
		return sandboxlocal.New(workDir), policy, nil
	}
}

// UnconfinedRunner returns a runner that executes commands directly on
// the host with the full environment (no OS-level sandbox), used by the
// execd child for YOLO-mode start requests.
func UnconfinedRunner(workDir string) coresandbox.Runner {
	return sandboxlocal.New(workDir)
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
