package bindings

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/execpolicy"
	ocsandbox "github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/telemetry"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/gitx"
)

// Diagnostics exposes environment/health information.
type Diagnostics struct {
	core *core.Core
}

// NewDiagnosticsBinding wires the diagnostics binding.
func NewDiagnosticsBinding(c *core.Core) *Diagnostics {
	return &Diagnostics{core: c}
}

// Report is the environment summary.
type Report struct {
	Version             string `json:"version"`
	GoVersion           string `json:"go_version"`
	NodeVersion         string `json:"node_version"`
	GitVersion          string `json:"git_version"`
	Platform            string `json:"platform"`
	Arch                string `json:"arch"`
	WorkDir             string `json:"work_dir"`
	UserDir             string `json:"user_dir"`
	ConfigValid         bool   `json:"config_valid"`
	ConfigError         string `json:"config_error,omitempty"`
	InferenceConfigured bool   `json:"inference_configured"`
	GitRepo             bool   `json:"git_repo"`
	GitBranch           string `json:"git_branch,omitempty"`
	SessionCount        int    `json:"session_count"`
	ActiveRuns          int    `json:"active_runs"`
	SandboxBackend      string `json:"sandbox_backend"`
	SandboxAvailable    bool   `json:"sandbox_available"`
	UsageTotalTokens    int64  `json:"usage_total_tokens"`
}

// Diagnostics gathers the environment/health summary.
func (b *Diagnostics) Diagnostics() Report {
	ctx := b.core.Shell.Context()
	rep := Report{
		Version:     telemetry.ServiceVersion,
		GoVersion:   goruntime.Version(),
		NodeVersion: commandVersion(ctx, 3*time.Second, "node", "--version"),
		GitVersion:  commandVersion(ctx, 3*time.Second, "git", "--version"),
		Platform:    goruntime.GOOS,
		Arch:        goruntime.GOARCH,
		WorkDir:     b.core.ActiveWorkDir(),
		UserDir:     b.core.UserDir,
	}
	if mgr, err := config.Open(config.Options{UserDir: b.core.UserDir}); err == nil {
		if view, err := mgr.Load(ctx); err != nil {
			rep.ConfigError = err.Error()
		} else {
			rep.ConfigValid = true
			if configured, err := config.RouterConfigured(view.Document); err == nil {
				rep.InferenceConfigured = configured
			} else {
				rep.ConfigError = err.Error()
			}
		}
	} else {
		rep.ConfigError = err.Error()
	}
	if wd := rep.WorkDir; wd != "" {
		if root := gitx.Root(wd); root != "" {
			rep.GitRepo = true
			branch, _ := gitx.RunBounded(
				ctx, root, 1024, 5*time.Second,
				"branch", "--show-current",
			)
			rep.GitBranch = strings.TrimSpace(branch)
		}
	}
	if h := b.core.Runtime.Current(); h != nil {
		rep.ActiveRuns = len(h.ActiveRuns())
		if h.Sessions() != nil {
			if metas, err := h.Sessions().List(); err == nil {
				rep.SessionCount = len(metas)
			}
		}
	}
	rep.SandboxBackend, rep.SandboxAvailable = sandboxBackend()
	if store := b.core.Runtime.Usage(); store != nil {
		if rows, err := store.Summary(ctx); err == nil {
			for _, r := range rows {
				rep.UsageTotalTokens += r.InputTokens + r.OutputTokens +
					r.CacheReadTokens + r.ReasoningTokens
			}
		}
	}
	return rep
}

// sandboxBackend reports the OS isolation layer for this platform.
func sandboxBackend() (string, bool) {
	switch goruntime.GOOS {
	case "darwin":
		_, err := exec.LookPath("sandbox-exec")
		return "seatbelt", err == nil
	case "linux":
		_, err := exec.LookPath("bwrap")
		return "bwrap", err == nil
	default:
		return "local", true
	}
}

// commandVersion runs one version probe best-effort.
func commandVersion(
	ctx context.Context,
	timeout time.Duration,
	name string,
	args ...string,
) string {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// PolicyDecision reports whether one command is allowed by the live
// exec policy.
type PolicyDecision struct {
	Command string   `json:"command"`
	Allowed bool     `json:"allowed"`
	Rules   []string `json:"rules"`
}

// EvaluateCommandPolicy checks one command against the live policy.
func (b *Diagnostics) EvaluateCommandPolicy(
	command string,
) (PolicyDecision, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return PolicyDecision{}, os.ErrInvalid
	}
	h := b.core.Runtime.Current()
	if h == nil || h.Controller() == nil || h.Controller().Runtime() == nil {
		return PolicyDecision{}, errNotReady("diagnostics")
	}
	value, ok := h.Controller().Runtime().Resource("execpolicy")
	if !ok {
		return PolicyDecision{}, errNotReady("execpolicy")
	}
	mgr, ok := value.(*execpolicy.Manager)
	if !ok {
		return PolicyDecision{}, errNotReady("execpolicy")
	}
	rules := mgr.Rules()
	allowlist, err := coresandbox.NewAllowlist(rules...)
	if err != nil {
		return PolicyDecision{}, err
	}
	allowed := allowlist.Matches(coresandbox.ExecRequest{
		Command: fields[0],
		Args:    fields[1:],
	})
	return PolicyDecision{Command: command, Allowed: allowed, Rules: rules}, nil
}

// CacheClearResult reports removed cache directories.
type CacheClearResult struct {
	Dirs  []string `json:"dirs"`
	Bytes int64    `json:"bytes"`
}

// ClearCaches removes cache directories and reports freed bytes.
func (b *Diagnostics) ClearCaches() (CacheClearResult, error) {
	dirs := []string{
		filepath.Join(b.core.DataDir, "cache", "tools"),
		filepath.Join(b.core.DataDir, "cache", "staged"),
	}
	if wd := b.core.ActiveWorkDir(); wd != "" {
		if layout, err := b.core.ResolveLayout(wd); err == nil {
			dirs = append(dirs, layout.CacheDir)
		}
	}
	var bytes int64
	for _, dir := range dirs {
		bytes += dirSize(dir)
		_ = os.RemoveAll(dir)
	}
	return CacheClearResult{Dirs: dirs, Bytes: bytes}, nil
}

// SandboxProbeResult reports one sandbox self-test.
type SandboxProbeResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// RunSandboxProbe verifies the platform sandbox can start a command.
func (b *Diagnostics) RunSandboxProbe() SandboxProbeResult {
	ctx := b.core.Shell.Context()
	workDir := b.core.ActiveWorkDir()
	if workDir == "" {
		return SandboxProbeResult{Error: "no workspace selected"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	runner, _, err := ocsandbox.SandboxRunner(
		probeCtx, workDir, ocsandbox.SandboxPolicy{},
	)
	if err != nil {
		return SandboxProbeResult{Error: err.Error()}
	}
	defer func() { _ = runner.Close() }()
	sess, err := runner.Start(probeCtx, coresandbox.SessionSpec{
		ID:   "diagnostics-probe",
		Argv: []string{"echo", "opencraft-sandbox-ok"},
	})
	if err != nil {
		return SandboxProbeResult{Error: err.Error()}
	}
	defer func() { _ = sess.Close() }()
	out, readErr := sess.Read(probeCtx, 0, 64*1024)
	var output strings.Builder
	if readErr == nil {
		for _, chunk := range out.Chunks {
			output.Write(chunk.Data)
		}
	}
	exit, waitErr := sess.Wait(probeCtx)
	ok := readErr == nil && waitErr == nil && exit.Code == 0 &&
		strings.Contains(output.String(), "opencraft-sandbox-ok")
	result := SandboxProbeResult{OK: ok, Output: output.String()}
	if !ok {
		switch {
		case waitErr != nil:
			result.Error = waitErr.Error()
		case readErr != nil:
			result.Error = readErr.Error()
		case exit.Code != 0:
			result.Error = "exit code " + strconv.Itoa(exit.Code)
		default:
			result.Error = "unexpected output"
		}
	}
	return result
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
