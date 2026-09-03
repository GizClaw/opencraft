package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	"sigs.k8s.io/yaml"

	"github.com/GizClaw/flowcraft/core/telemetry"
	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
	ocsandbox "github.com/GizClaw/opencraft/internal/sandbox"
	otellog "go.opentelemetry.io/otel/log"
)

// DiagnosticsReport is the environment/health summary shown on the
// settings Diagnostics tab.
type DiagnosticsReport struct {
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

// SandboxProbeResult reports one sandbox self-test run.
type SandboxProbeResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// PolicyDecision is the result of evaluating one command against the
// live exec policy (static rules + project approvals).
type PolicyDecision struct {
	Command string   `json:"command"`
	Allowed bool     `json:"allowed"`
	Rules   []string `json:"rules"`
}

// CacheClearResult reports which cache directories were removed.
type CacheClearResult struct {
	Dirs  []string `json:"dirs"`
	Bytes int64    `json:"bytes"`
}

// Diagnostics gathers the environment/health summary for the UI.
func (a *App) Diagnostics() DiagnosticsReport {
	ctx := a.appContext()
	wd := a.snapshotWorkDir()
	rep := DiagnosticsReport{
		Version:   app.ServiceVersion,
		GoVersion: goruntime.Version(),
		Platform:  goruntime.GOOS,
		Arch:      goruntime.GOARCH,
		WorkDir:   wd,
		UserDir:   a.userDir,
	}
	rep.NodeVersion = commandVersion(ctx, 3*time.Second, "node", "--version")
	rep.GitVersion = commandVersion(ctx, 3*time.Second, "git", "--version")

	// Without a workspace there is no project layer to discover; skip
	// it so config.Open never falls back to the process cwd (which is
	// "/" for Finder-launched apps).
	if mgr, err := config.Open(config.Options{
		WorkDir:          wd,
		UserDir:          a.userDir,
		SkipProjectLayer: wd == "",
	}); err == nil {
		if _, err := mgr.Load(ctx); err != nil {
			rep.ConfigError = err.Error()
		} else {
			rep.ConfigValid = true
		}
	} else {
		rep.ConfigError = err.Error()
	}
	if needed, err := config.InferenceNeeded(a.userDir); err == nil {
		rep.InferenceConfigured = !needed
	}

	if wd != "" {
		if root := gitRepoRoot(wd); root != "" {
			rep.GitRepo = true
			rep.GitBranch = gitBranch(ctx, root)
		}
	}
	if a.sessions != nil {
		if metas, err := a.sessions.List(); err == nil {
			rep.SessionCount = len(metas)
		}
	}
	a.mu.Lock()
	rep.ActiveRuns = len(a.turns)
	usageStore := a.usage
	a.mu.Unlock()
	rep.SandboxBackend, rep.SandboxAvailable = sandboxBackend()
	if usageStore != nil {
		if rows, err := usageStore.Summary(a.appContext()); err == nil {
			for _, r := range rows {
				rep.UsageTotalTokens += r.InputTokens + r.OutputTokens +
					r.CacheReadTokens + r.ReasoningTokens
			}
		}
	}
	return rep
}

// RunSandboxProbe starts a trivial command through the platform sandbox
// runner (seatbelt/bwrap) and verifies its output, so the UI can
// confirm the OS isolation layer actually works on this machine.
func (a *App) RunSandboxProbe() SandboxProbeResult {
	if strings.TrimSpace(a.snapshotWorkDir()) == "" {
		return SandboxProbeResult{Error: "no workspace selected"}
	}
	ctx, cancel := context.WithTimeout(a.appContext(), 30*time.Second)
	defer cancel()
	runner, _, err := ocsandbox.SandboxRunner(
		ctx,
		a.snapshotWorkDir(),
		ocsandbox.SandboxPolicy{},
	)
	if err != nil {
		return SandboxProbeResult{Error: err.Error()}
	}
	defer func() { _ = runner.Close() }()
	sess, err := runner.Start(ctx, coresandbox.SessionSpec{
		ID:   "diagnostics-probe",
		Argv: []string{"echo", "opencraft-sandbox-ok"},
	})
	if err != nil {
		return SandboxProbeResult{Error: err.Error()}
	}
	defer func() { _ = sess.Close() }()
	out, readErr := sess.Read(ctx, 0, 64*1024)
	var output strings.Builder
	if readErr == nil {
		for _, chunk := range out.Chunks {
			output.Write(chunk.Data)
		}
	}
	exit, waitErr := sess.Wait(ctx)
	ok := readErr == nil && waitErr == nil && exit.Code == 0 &&
		strings.Contains(output.String(), "opencraft-sandbox-ok")
	result := SandboxProbeResult{
		OK:     ok,
		Output: output.String(),
	}
	if !ok {
		if waitErr != nil {
			result.Error = waitErr.Error()
		} else if readErr != nil {
			result.Error = readErr.Error()
		} else if exit.Code != 0 {
			result.Error = "exit code " + strconv.Itoa(exit.Code)
		} else {
			result.Error = "unexpected output"
		}
	}
	return result
}

// EvaluateCommandPolicy checks one command against the live exec
// policy: static allowed_commands from the merged config plus the
// project approvals file. Allowed means it would run without asking.
func (a *App) EvaluateCommandPolicy(command string) (PolicyDecision, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return PolicyDecision{}, errors.New("command is empty")
	}
	wd := a.snapshotWorkDir()
	rules, err := a.execPolicyRules(wd)
	if err != nil {
		return PolicyDecision{}, err
	}
	allowlist, err := coresandbox.NewAllowlist(rules...)
	if err != nil {
		return PolicyDecision{}, err
	}
	allowed := allowlist.Matches(coresandbox.ExecRequest{
		Command: fields[0],
		Args:    fields[1:],
	})
	return PolicyDecision{
		Command: command,
		Allowed: allowed,
		Rules:   rules,
	}, nil
}

// ClearCaches removes the known cache directories (tool truncate
// cache, staged skills, project cache) and reports what was freed.
func (a *App) ClearCaches() (CacheClearResult, error) {
	dataDir, err := config.UserDataDir()
	if err != nil {
		return CacheClearResult{}, err
	}
	dirs := []string{
		filepath.Join(dataDir, "cache", "tools"),
		filepath.Join(dataDir, "cache", "staged"),
	}
	if wd := a.snapshotWorkDir(); wd != "" {
		dirs = append(dirs, filepath.Join(wd, ".opencraft", "cache"))
	}
	var bytes int64
	for _, dir := range dirs {
		bytes += dirSize(dir)
		if err := os.RemoveAll(dir); err != nil {
			telemetry.Warn(a.appContext(), "diagnostics: clear cache failed",
				otellog.String("dir", dir),
				otellog.String("error", err.Error()))
		}
	}
	return CacheClearResult{Dirs: dirs, Bytes: bytes}, nil
}

// execPolicyRules loads the static allowed_commands from the merged
// config and appends the project approvals file entries.
func (a *App) execPolicyRules(wd string) ([]string, error) {
	mgr, err := config.Open(config.Options{
		WorkDir:          wd,
		UserDir:          a.userDir,
		SkipProjectLayer: wd == "",
	})
	if err != nil {
		return nil, err
	}
	view, err := mgr.Load(a.appContext())
	if err != nil {
		return nil, err
	}
	var rules []string
	if res, ok := view.Document.Resources["execpolicy"]; ok {
		var settings struct {
			AllowedCommands []string `json:"allowed_commands"`
		}
		if len(res.Settings) > 0 {
			if err := json.Unmarshal(res.Settings, &settings); err != nil {
				return nil, err
			}
		}
		rules = append(rules, settings.AllowedCommands...)
	}
	data, err := os.ReadFile(filepath.Join(wd, ".opencraft", "config", "approvals.yaml"))
	if err == nil {
		var approvals struct {
			Allow []string `json:"allow"`
		}
		if err := yaml.Unmarshal(data, &approvals); err != nil {
			return nil, err
		}
		rules = append(rules, approvals.Allow...)
	}
	return rules, nil
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

// gitBranch returns the current branch of the repo at root.
func gitBranch(ctx context.Context, root string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(
		ctx, "git", "-C", root, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

// dirSize sums the sizes of all regular files under dir (0 when absent).
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
