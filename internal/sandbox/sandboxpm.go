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
	sbwindows "github.com/GizClaw/flowcraft/core/sandbox/windows"

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

// defaultEnvAllow is the minimal environment surface sandboxed
// commands inherit when a deployment declares no env_policy. Provider
// API keys and other secrets in the parent environment are NOT on the
// list, so sandboxed (possibly untrusted) project code cannot read
// them from env. Projects that need more can declare env_policy in
// their config layer.
var defaultEnvAllow = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP",
	"TERM", "PWD", "HOSTNAME",
	"LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "LC_TIME",
	"LC_NUMERIC", "LC_COLLATE", "LC_MONETARY",
	"EDITOR", "VISUAL", "NO_COLOR", "COLORTERM",
	"OPEN_CRAFT_WORKDIR", "OPEN_CRAFT_CACHE", "OPEN_CRAFT_DATA_DIR",
	"GOPATH", "GOROOT", "GOMODCACHE", "GOCACHE", "GOTOOLCHAIN",
	"GOENV", "GOPROXY", "GOSUMDB", "GOFLAGS", "CGO_ENABLED", "CC", "CXX",
	"NODE_ENV",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "no_proxy",
	"SSH_AUTH_SOCK",
}

// DefaultEnvPolicy returns the environment policy applied when a
// deployment declares none: a curated allowlist instead of wholesale
// inheritance. It replaces nil Allow (inherit everything) with a
// bounded set, closing the "sandboxed command reads the parent's API
// keys" gap for the default configuration.
func DefaultEnvPolicy() coresandbox.EnvPolicy {
	return coresandbox.EnvPolicy{Allow: append([]string(nil), defaultEnvAllow...)}
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
	} else {
		policy = DefaultEnvPolicy()
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
	case "windows":
		// flowcraft v0.2.2 Windows backend with OS-level write
		// confinement: the child runs under a restricted Low-integrity
		// token and can only write inside the workspace/root and the
		// configured writable paths. The backend does not combine
		// confinement with ConPTY TTY sessions yet, so interactive
		// sessions stay disabled on Windows and the advertised
		// capability surface is kept honest (issue #38).
		runner, err := sbwindows.New(workDir,
			sbwindows.WithWriteConfinement(),
			sbwindows.WithWritablePaths(writable...))
		if err != nil {
			return nil, coresandbox.EnvPolicy{}, fmt.Errorf(
				"opencraft sandbox: windows: %w", err)
		}
		return noTTYRunner{runner}, policy, nil
	default:
		return sandboxlocal.New(workDir), policy, nil
	}
}

// UnconfinedRunner returns a runner that executes commands directly on
// the host with the full environment (no OS-level sandbox), used by the
// execd child for YOLO-mode start requests.
func UnconfinedRunner(workDir string) (coresandbox.Runner, error) {
	if goruntime.GOOS == "windows" {
		// Same job-object backend as the confined runner, but without
		// write confinement (YOLO keeps its full-host-access
		// contract). Interactive sessions stay disabled on Windows.
		runner, err := sbwindows.New(workDir)
		if err != nil {
			return nil, fmt.Errorf("opencraft sandbox: windows (unconfined): %w", err)
		}
		return noTTYRunner{runner}, nil
	}
	return sandboxlocal.New(workDir), nil
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
