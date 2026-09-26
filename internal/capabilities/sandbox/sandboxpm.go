package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	sbwindows "github.com/GizClaw/flowcraft/core/sandbox/windows"

	"github.com/GizClaw/opencraft/internal/foundation/config"
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
// (seatbelt on macOS, bwrap on Linux, local elsewhere) against the
// global user-level cache directory. It is the fallback for a child
// bound without a resolved cache root; the desktop and headless parents
// always send one (SandboxPolicy.cache_dir).
func SandboxRunner(
	ctx context.Context,
	workDir string,
	pol SandboxPolicy,
) (coresandbox.Runner, coresandbox.EnvPolicy, error) {
	return SandboxRunnerForCache(ctx, workDir, "", pol)
}

// SandboxRunnerForCache builds the platform sandbox runner against an
// explicitly injected cache root: the parent resolves ${ocraft:CACHE}
// from the state root it was launched with, so a dev-profile parent
// cannot push the child into the installed app's cache. An empty
// cacheDir falls back to the global user data root (SandboxRunner's
// historical behaviour).
func SandboxRunnerForCache(
	ctx context.Context,
	workDir, cacheDir string,
	pol SandboxPolicy,
) (coresandbox.Runner, coresandbox.EnvPolicy, error) {
	if strings.TrimSpace(cacheDir) == "" {
		dataDir, err := config.UserDataDir()
		if err != nil {
			return nil, coresandbox.EnvPolicy{}, err
		}
		cacheDir = filepath.Join(dataDir, "cache")
	}
	return SandboxRunnerWithCache(ctx, workDir, cacheDir, pol)
}

// SandboxRunnerWithCache builds the platform sandbox runner with an
// explicitly injected cache root (seatbelt on macOS, bwrap on Linux,
// local elsewhere). Writable paths are the injected cache root plus
// the project-configured writable_paths; the environment policy is
// the configured policy verbatim. Callers resolve the workspace
// layout and pass its cache directory; sandbox construction failures
// are fatal: silently falling back to the local runner would let the
// parent believe commands are isolated when they are not, so the
// child fails closed instead.
func SandboxRunnerWithCache(
	_ context.Context,
	workDir, cacheDir string,
	pol SandboxPolicy,
) (coresandbox.Runner, coresandbox.EnvPolicy, error) {
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
	// Same construction site as the parent-side factory (backend.go):
	// the child's backend must be the one the parent's diagnostics
	// page reports.
	runner, err := hostBackend(workDir, writable)
	if err != nil {
		return nil, coresandbox.EnvPolicy{}, err
	}
	return runner, policy, nil
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
