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
//
// A section that leaves allow out — including a section that only
// injects — resolves to the curated default allowlist, never to the
// core's nil form ("inherit the whole host environment"); an explicit
// empty list means "inherit nothing".
type EnvPolicyConfig struct {
	// Allow deliberately marshals without omitempty: nil becomes null
	// and a non-nil empty list becomes [], so a round trip through
	// JSON keeps the load-bearing nil-vs-empty distinction instead of
	// collapsing "inherit nothing" back into the default allowlist.
	Allow  []string          `json:"allow"`
	Inject map[string]string `json:"inject,omitempty"`
}

// resolve turns the configured section into the policy the runners
// enforce. The nil / empty distinction is load-bearing: to the core a
// nil Allow means "inherit the whole host environment", so a nil left
// in place here would silently widen an inject-only policy into full
// inheritance of the parent's provider keys. nil resolves to the
// curated default allowlist; a non-nil empty slice stays empty.
func (c *EnvPolicyConfig) resolve() coresandbox.EnvPolicy {
	policy := DefaultEnvPolicy()
	if c == nil {
		return policy
	}
	if c.Allow != nil {
		policy.Allow = c.Allow
	}
	policy.Inject = c.Inject
	return policy
}

// SandboxPolicy is the sandbox policy handed from the parent process
// to the execd child: project-configured writable paths plus the
// environment policy. The execd wire is its own proto message
// (capabilities/execd); this shape is what the runner constructors take
// in-process, and it marshals losslessly (nil allow stays null, an
// explicit empty list stays []), so a caller that does serialize it
// cannot silently widen an inject-only policy.
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
// the execd child. The environment policy travels resolved: the child
// never sees the nil-allow form that the core would read as "inherit
// everything".
func (s HostSandboxSettings) SandboxPolicy() SandboxPolicy {
	env := s.Env()
	return SandboxPolicy{
		WritablePaths: s.WritablePaths,
		EnvPolicy: &EnvPolicyConfig{
			Allow:  env.Allow,
			Inject: env.Inject,
		},
	}
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
	policy := pol.EnvPolicy.resolve()
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
	goos := goruntime.GOOS
	if goos == "windows" {
		// Same job-object backend as the confined runner, but without
		// write confinement (YOLO keeps its full-host-access
		// contract). Interactive sessions stay disabled on Windows.
		runner, err := sbwindows.New(workDir)
		if err != nil {
			return nil, fmt.Errorf("opencraft sandbox: windows (unconfined): %w", err)
		}
		return withPlatformCapabilities(goos, runner), nil
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
