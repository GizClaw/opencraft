package bindings

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	coresandbox "github.com/GizClaw/flowcraft/core/sandbox"
	flowtelemetry "github.com/GizClaw/flowcraft/core/telemetry"

	octelemetry "github.com/GizClaw/opencraft/internal/capabilities/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/execd"
	"github.com/GizClaw/opencraft/internal/capabilities/execpolicy"
	ocsandbox "github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/envpath"
	"github.com/GizClaw/opencraft/internal/foundation/utils/gitx"
	"github.com/GizClaw/opencraft/internal/foundation/utils/shelldetect"
	"github.com/GizClaw/opencraft/internal/foundation/version"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// Diagnostics exposes environment/health information.
type Diagnostics struct {
	core *core.Core
}

// NewDiagnosticsBinding wires the diagnostics binding.
func NewDiagnosticsBinding(c *core.Core) *Diagnostics {
	return &Diagnostics{core: c}
}

// ExecPoolDTO is the diagnostics view of the exec supervisor pool: the
// persisted knobs plus the live idle/active child counts.
type ExecPoolDTO struct {
	Prewarm     int `json:"prewarm"`
	MaxIdle     int `json:"maxIdle"`
	MaxActive   int `json:"maxActive"`
	IdleMinutes int `json:"idleMinutes"`
	Idle        int `json:"idle"`
	Active      int `json:"active"`
}

// ExecPool reports the configured pool settings and live counts.
func (b *Diagnostics) ExecPool() ExecPoolDTO {
	dto := execPoolDTO(b.core.Shell.ExecPool())
	if pool := execd.DefaultPool(); pool != nil {
		dto.Idle, dto.Active = pool.Stats()
	}
	return dto
}

// SetExecPool persists new pool settings and applies them to the live
// pool. Existing children keep serving; future leases and reaping use
// the new values.
func (b *Diagnostics) SetExecPool(
	prewarm, maxIdle, maxActive, idleMinutes int,
) (ExecPoolDTO, error) {
	settings := execd.NormalizePoolSettings(execd.PoolSettings{
		Prewarm:   prewarm,
		MaxIdle:   maxIdle,
		MaxActive: maxActive,
		IdleTTL:   time.Duration(idleMinutes) * time.Minute,
	})
	if err := b.core.Shell.SetExecPool(settings); err != nil {
		return ExecPoolDTO{}, err
	}
	return b.ExecPool(), nil
}

func execPoolDTO(settings execd.PoolSettings) ExecPoolDTO {
	prefs := core.PoolPrefs(settings)
	return ExecPoolDTO{
		Prewarm:     prefs.Prewarm,
		MaxIdle:     prefs.MaxIdle,
		MaxActive:   prefs.MaxActive,
		IdleMinutes: prefs.IdleMinutes,
	}
}

// PathSegmentDTO is one entry of the resolved process PATH. Source is
// "prepend" (user override), "inherited" (the PATH the app was launched
// with) or "candidate" (a standard install directory the resolver added).
type PathSegmentDTO struct {
	Dir     string `json:"dir"`
	Source  string `json:"source"`
	Present bool   `json:"present"`
}

// PathEnvironmentDTO is the diagnostics view of the process PATH: the
// value every spawn inherits, where each entry came from, and what the
// resolver refused or could not find.
type PathEnvironmentDTO struct {
	Path     string           `json:"path"`
	Segments []PathSegmentDTO `json:"segments"`
	// Prepend is the persisted override, i.e. the editor's value.
	Prepend []string `json:"prepend"`
	// Rejected lists override entries dropped because they are not
	// absolute directories.
	Rejected []string `json:"rejected"`
	// Missing lists candidate directories that do not exist on this
	// machine.
	Missing []string `json:"missing"`
	// Reloaded reports whether the runtime reload ran through, so MCP
	// servers re-attach with the new PATH. False means the reload failed
	// (the reason goes to the log); the PATH change itself still applies
	// to every future spawn, including the next runtime build.
	Reloaded bool `json:"reloaded"`
}

// PathEnvironment reports the PATH this process runs with. It is a read
// of the last resolution, not a new one.
func (b *Diagnostics) PathEnvironment() PathEnvironmentDTO {
	if report, ok := b.core.PathReport(); ok {
		return b.pathEnvironmentDTO(report.Plan, report.Rejected, report.Missing)
	}
	// No install ran in this process (an embedded or test host): describe
	// what the current environment would resolve to without writing it
	// back.
	plan := envpath.Inspect(envpath.Options{
		Prepend: b.core.Shell.PathPrepend(),
	})
	return b.pathEnvironmentDTO(plan, plan.Rejected, plan.Missing)
}

// SetPathPrepend persists the PATH override and applies it: resolution
// runs again and the runtime reloads so MCP servers attach with the new
// environment. Already-running sessions keep the PATH of the process they
// were spawned with.
func (b *Diagnostics) SetPathPrepend(dirs []string) (PathEnvironmentDTO, error) {
	if err := b.core.Shell.SetPathPrepend(dirs); err != nil {
		return PathEnvironmentDTO{}, err
	}
	return b.ResolvePath()
}

// ResolvePath re-runs PATH resolution from the current environment and the
// persisted override, then reloads the runtime. A failed reload is logged
// and reported as Reloaded=false rather than failing the call: the PATH
// change itself already took effect for every future spawn.
func (b *Diagnostics) ResolvePath() (PathEnvironmentDTO, error) {
	ctx := b.core.Shell.Context()
	resolved, err := envpath.Install(envpath.Options{
		Prepend: b.core.Shell.PathPrepend(),
	})
	if err != nil {
		return PathEnvironmentDTO{}, err
	}
	b.core.SetPathReport(resolved)
	dto := b.pathEnvironmentDTO(resolved.Plan, resolved.Rejected, resolved.Missing)
	reloadCtx := host.WithAssemblyReason(ctx, host.ReasonPathSave)
	if err := b.core.ApplyDocumentReload(reloadCtx); err != nil {
		flowtelemetry.WarnErr(ctx, "desktop diagnostics: runtime reload failed", err)
		return dto, nil
	}
	dto.Reloaded = true
	return dto, nil
}

// pathEnvironmentDTO assembles the view from one resolution.
func (b *Diagnostics) pathEnvironmentDTO(
	plan envpath.Plan,
	rejected, missing []string,
) PathEnvironmentDTO {
	dto := PathEnvironmentDTO{
		Path:     plan.Path,
		Prepend:  listOrEmpty(b.core.Shell.PathPrepend()),
		Rejected: listOrEmpty(rejected),
		Missing:  listOrEmpty(missing),
	}
	dto.Segments = make([]PathSegmentDTO, 0, len(plan.Segments))
	for _, segment := range plan.Segments {
		dto.Segments = append(dto.Segments, PathSegmentDTO{
			Dir:     segment.Dir,
			Source:  segment.Source,
			Present: segment.Present,
		})
	}
	return dto
}

// listOrEmpty keeps the wire shape stable: a nil slice marshals to null,
// and the renderer reads these fields as lists.
func listOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// TelemetryExportDTO is the diagnostics view of the OTLP export sink.
// Header values are credentials and stay in the host, so the DTO carries
// header names only.
type TelemetryExportDTO struct {
	// Enabled is the user switch: may capability plugins install their
	// own export sink?
	Enabled bool `json:"enabled"`
	// Configured reports whether an OTLP endpoint is active, whoever
	// installed it.
	Configured  bool     `json:"configured"`
	Endpoint    string   `json:"endpoint"`
	Insecure    bool     `json:"insecure"`
	HeaderNames []string `json:"headerNames"`
	// Owner is the plugin that installed the sink, empty when the
	// application configuration owns it.
	Owner string `json:"owner"`
}

// TelemetryExport reports where the app currently exports telemetry and
// whether capability plugins may point it at their own collector.
func (b *Diagnostics) TelemetryExport() TelemetryExportDTO {
	state := b.core.PluginTelemetryState()
	return TelemetryExportDTO{
		Enabled:     state.Enabled,
		Configured:  state.Configured,
		Endpoint:    state.Endpoint,
		Insecure:    state.Insecure,
		HeaderNames: state.HeaderNames,
		Owner:       state.Owner,
	}
}

// SetTelemetryExport toggles the plugin export switch. Disabling it
// drops the sink a plugin installed and remembers it, so enabling the
// switch again re-installs it without waiting for the plugin to
// re-configure.
func (b *Diagnostics) SetTelemetryExport(enabled bool) error {
	return b.core.SetPluginTelemetryExport(enabled)
}

// PerfProbe reports whether the renderer-side performance sampler is
// switched on. It is the Diagnostics-tab switch for the frontend
// sampler: dom_nodes, frame gaps, stream-flush timings and the number of
// loaded transcript rows, every 30s.
func (b *Diagnostics) PerfProbe() bool {
	return b.core.Shell.PerfProbe()
}

// SetPerfProbe persists the renderer-side sampler switch. The page
// starts or stops the sampler and the host remembers the choice, so a
// support session survives a restart.
func (b *Diagnostics) SetPerfProbe(enabled bool) error {
	return b.core.Shell.SetPerfProbe(enabled)
}

// HTTPProbeDTO is the diagnostics view of the provider round-trip
// probe: the persisted switch, the environment override, whether the
// transport is wrapped right now and, when it is not, the MCP
// configuration that keeps it out.
type HTTPProbeDTO struct {
	Enabled bool   `json:"enabled"`
	Env     bool   `json:"env"`
	Active  bool   `json:"active"`
	Blocker string `json:"blocker"`
}

// HTTPProbe reports how the provider round-trip probe is wired. The
// probe records "request dispatched" and "response headers received"
// for every provider call — the split between local assembly and
// provider time that a per-turn latency number cannot show. The MCP
// client cannot run under a wrapped transport, so an HTTP MCP server
// parks the probe; Blocker names it when that is the case.
func (b *Diagnostics) HTTPProbe() HTTPProbeDTO {
	state := b.core.HTTPProbeState()
	return HTTPProbeDTO{
		Enabled: state.Enabled,
		Env:     state.Env,
		Active:  state.Active,
		Blocker: state.Blocker,
	}
}

// SetHTTPProbe persists the switch and applies it: turning it on wraps
// the process transport and reloads the runtime so provider clients
// rebuild against it; turning it off restores the transport
// immediately.
func (b *Diagnostics) SetHTTPProbe(enabled bool) (HTTPProbeDTO, error) {
	if err := b.core.SetHTTPProbe(b.core.Shell.Context(), enabled); err != nil {
		return HTTPProbeDTO{}, err
	}
	return b.HTTPProbe(), nil
}

var (
	frontendVitalsDuration = octelemetry.MustFloat64Histogram(
		"frontend.vitals.duration_ms",
		metric.WithUnit("ms"),
		metric.WithDescription(
			"Renderer web vitals and navigation durations"))
)

// FrontendPerfSample is one renderer performance measurement.
type FrontendPerfSample struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

// frontendDurationMetrics are the renderer samples recorded on the duration
// histogram; everything else is only logged so a compromised renderer cannot
// fabricate instrument names.
var frontendDurationMetrics = map[string]bool{
	"fid":                true,
	"lcp":                true,
	"inp":                true,
	"dom_content_loaded": true,
	"load":               true,
}

// ReportFrontendPerf records renderer web-vitals and navigation samples:
// every sample is logged, and known samples are also recorded on the
// opencraft frontend metric instruments so they join the same OTLP export as
// backend metrics.
func (b *Diagnostics) ReportFrontendPerf(samples []FrontendPerfSample) {
	ctx := b.core.Shell.Context()
	for _, sample := range samples {
		unit := sample.Unit
		if unit == "" {
			unit = "1"
		}
		flowtelemetry.Info(ctx, "frontend rum: "+sample.Name,
			log.Float64("value", sample.Value),
			log.String("unit", unit))
		if mgr := b.core.Runtime.Manager(); mgr != nil {
			mgr.RecordMetric(ctx, "frontend."+sample.Name,
				sample.Value, map[string]string{"unit": unit})
		}
		if frontendDurationMetrics[sample.Name] {
			frontendVitalsDuration.Record(ctx, sample.Value,
				metric.WithAttributes(
					attribute.String("metric", sample.Name)))
		}
	}
}

// HeapProfileResult is where one captured heap profile landed on disk.
type HeapProfileResult struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// CaptureHeapProfile writes a runtime/pprof heap profile next to the app
// logs so a long-running process can be analyzed offline with
// `go tool pprof -inuse_space <binary> <file>`.
//
// The profile is taken after a forced GC: what matters for a memory
// investigation is what the process still retains, and without the
// collection the sample is dominated by allocation churn that the
// runtime is about to reclaim anyway.
func (b *Diagnostics) CaptureHeapProfile() (HeapProfileResult, error) {
	dir := filepath.Join(b.core.DataDir, "diagnostics")
	result, err := writeHeapProfile(dir, time.Now().UTC())
	if err != nil {
		return HeapProfileResult{}, err
	}
	flowtelemetry.Info(b.core.Shell.Context(), "diagnostics: heap profile captured",
		log.String("path", result.Path),
		log.Int64("bytes", result.Bytes))
	return result, nil
}

// writeHeapProfile is the testable half: it creates the directory, forces
// a GC, and writes the profile.
func writeHeapProfile(
	dir string,
	now time.Time,
) (HeapProfileResult, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return HeapProfileResult{}, err
	}
	path := filepath.Join(dir,
		fmt.Sprintf("heap-%s.pprof", now.Format("20060102-150405")))
	f, err := os.Create(path)
	if err != nil {
		return HeapProfileResult{}, err
	}
	goruntime.GC()
	writeErr := pprof.WriteHeapProfile(f)
	closeErr := f.Close()
	if writeErr != nil {
		return HeapProfileResult{}, writeErr
	}
	if closeErr != nil {
		return HeapProfileResult{}, closeErr
	}
	info, err := os.Stat(path)
	if err != nil {
		return HeapProfileResult{}, err
	}
	return HeapProfileResult{Path: path, Bytes: info.Size()}, nil
}

// MetricPoint is one local metric sample shown in the diagnostics panel.
type MetricPoint struct {
	Ts    int64             `json:"ts"`
	Value float64           `json:"value"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

// MetricRange returns one local metric series from the user-level metric
// store. fromMs/toMs are unix milliseconds (toMs 0 means no upper bound).
// An empty result is returned when the user database is not open.
func (b *Diagnostics) MetricRange(
	name string,
	fromMs, toMs int64,
	limit int,
) ([]MetricPoint, error) {
	mgr := b.core.Runtime.Manager()
	if mgr == nil {
		return nil, nil
	}
	store := mgr.MetricsStore()
	if store == nil {
		return nil, nil
	}
	samples, err := store.RangeNewest(
		b.core.Shell.Context(), name, fromMs, toMs, limit)
	if err != nil {
		return nil, err
	}
	points := make([]MetricPoint, 0, len(samples))
	for _, sample := range samples {
		points = append(points, MetricPoint{
			Ts: sample.Ts, Value: sample.Value, Attrs: sample.Attrs,
		})
	}
	return points, nil
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
	ExecShell           string `json:"exec_shell"`
	UsageTotalTokens    int64  `json:"usage_total_tokens"`
}

// RecoveryDTO is the diagnostics view of crash recovery: what the last
// pass did, and how much the active workspace's checkpoint table is
// holding right now.
//
// A run writes one checkpoint per completed wave and drops it once the
// turn has an archive row, so the table is normally empty between
// turns. Rows left behind belong either to a live run (a sibling
// process the pass deliberately skips) or to a turn whose archive write
// never landed — the ones the pass materializes as interrupted turns
// (see orchestration/host/recover.go).
type RecoveryDTO struct {
	// Workspace names the store these numbers came from.
	Workspace string `json:"workspace,omitempty"`
	// Ran reports whether an assembly ran a pass in this process.
	Ran bool `json:"ran"`
	// At is when that pass ran.
	At string `json:"at,omitempty"`
	// Recovered counts turns materialized as interrupted.
	Recovered int `json:"recovered"`
	// Archived counts checkpoints dropped because the turn already had
	// an archive row (an in-process stop).
	Archived int `json:"archived"`
	// Discarded counts checkpoints dropped as unusable: not a
	// conversation, a deleted conversation, or a board without a
	// reconstructable turn.
	Discarded int `json:"discarded"`
	// SkippedLive counts checkpoints a sibling process may still own.
	SkippedLive int `json:"skipped_live"`
	// Failed counts recovery writes that failed; the next pass retries
	// them.
	Failed int `json:"failed"`
	// Pending counts checkpoints the pass did not examine (its cap).
	Pending int `json:"pending"`
	// CheckpointRows is every row of the table; CheckpointRuns is the
	// assistant-run subset under state.RunCheckpointPrefix, which is
	// what the recovery pass examines. CheckpointBytes is their
	// encoded size.
	CheckpointRows  int   `json:"checkpoint_rows"`
	CheckpointRuns  int   `json:"checkpoint_runs"`
	CheckpointBytes int64 `json:"checkpoint_bytes"`
}

// Recovery reports the crash-recovery state of the active workspace.
// With no workspace assembled yet every counter is zero and Ran is
// false, which the card renders as "no pass yet" instead of a clean
// bill of health.
func (b *Diagnostics) Recovery() RecoveryDTO {
	dto := RecoveryDTO{}
	h := b.core.Runtime.Current()
	if h == nil {
		return dto
	}
	dto.Workspace = h.WorkDir()
	if report, ok := h.RecoveryReport(); ok {
		dto.Ran = true
		dto.At = report.At.UTC().Format(time.RFC3339)
		dto.Recovered = report.Recovered
		dto.Archived = report.Archived
		dto.Discarded = report.Discarded
		dto.SkippedLive = report.SkippedLive
		dto.Failed = report.Failed
		dto.Pending = report.Pending
	}
	if store := h.Sessions(); store != nil {
		stats, err := store.State().CheckpointStats(b.core.Shell.Context())
		if err != nil {
			// A store that cannot be read is what the pass would report
			// as failed; the card simply shows no checkpoint numbers.
			return dto
		}
		dto.CheckpointRows = stats.Rows
		dto.CheckpointRuns = stats.Runs
		dto.CheckpointBytes = stats.Bytes
	}
	return dto
}

// Diagnostics gathers the environment/health summary.
func (b *Diagnostics) Diagnostics() Report {
	ctx := b.core.Shell.Context()
	rep := Report{
		Version:     version.ServiceVersion,
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
	// The shell exec_command actually spawns through: the settings page
	// must not disagree with the tool description the model reads.
	rep.ExecShell = shelldetect.Detect(goruntime.GOOS).CommandLine()
	if store := b.core.Runtime.Usage(); store != nil {
		if rows, err := store.Summary(ctx); err == nil {
			for _, r := range rows {
				// Sum the provider-reported billed totals stored per
				// record instead of reconstructing a total from the
				// breakdown streams, whose inclusion rules differ by
				// provider (reasoning/cache may be inside input+output
				// or billed separately). Legacy rows rebuilt by
				// migration 004 fall back to input + output.
				rep.UsageTotalTokens += r.TotalTokens
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

// ConfigCompatRepair reports one user-layer compatibility repair.
type ConfigCompatRepair struct {
	// File is the user configuration layer that was inspected.
	File string `json:"file"`
	// Backup is the pre-repair copy, empty when nothing was removed.
	Backup string `json:"backup"`
	// Removed lists the YAML paths dropped from the layer, so the user
	// can see exactly which declarations fell away.
	Removed []string `json:"removed"`
}

// RepairConfigCompat drops the user-layer declarations that reference
// retired assembly variables (${env:OPEN_CRAFT_*}), so the built-in
// layer supplies them again. It touches only the user configuration
// layer, leaves a .bak copy behind, and reports what it removed; a layer
// without obsolete references is left byte-for-byte untouched.
func (b *Diagnostics) RepairConfigCompat() (ConfigCompatRepair, error) {
	res, err := config.RepairRetiredRefs(b.core.UserDir)
	if err != nil {
		return ConfigCompatRepair{}, err
	}
	// A nil slice marshals to JSON null, which the frontend would have to
	// guard against on every read; keep the wire shape an empty list.
	removed := res.Removed
	if removed == nil {
		removed = []string{}
	}
	return ConfigCompatRepair{
		File:    res.File,
		Backup:  res.Backup,
		Removed: removed,
	}, nil
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
		flowtelemetry.WarnErr(context.Background(),
			"desktop diagnostics: clear cache directory failed",
			os.RemoveAll(dir))
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
	// The probe exercises the same injected cache root the workspace
	// runtime uses; fall back to the global cache only when the
	// workspace layout cannot be resolved.
	cacheDir := filepath.Join(b.core.DataDir, "cache")
	if layout, err := b.core.ResolveLayout(workDir); err == nil {
		cacheDir = filepath.Join(layout.Root, "cache")
	}
	runner, _, err := ocsandbox.SandboxRunnerWithCache(
		probeCtx, workDir, cacheDir, ocsandbox.SandboxPolicy{},
	)
	if err != nil {
		return SandboxProbeResult{Error: err.Error()}
	}
	defer func() {
		flowtelemetry.WarnErr(probeCtx,
			"desktop diagnostics: close sandbox runner failed", runner.Close())
	}()
	sess, err := runner.Start(probeCtx, coresandbox.SessionSpec{
		ID:   "diagnostics-probe",
		Argv: []string{"echo", "opencraft-sandbox-ok"},
	})
	if err != nil {
		return SandboxProbeResult{Error: err.Error()}
	}
	defer func() {
		flowtelemetry.WarnErr(probeCtx,
			"desktop diagnostics: close sandbox session failed", sess.Close())
	}()
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
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	flowtelemetry.WarnErr(context.Background(),
		"desktop diagnostics: walk cache directory failed", err)
	return total
}

// maxFrontendErrorDetail bounds the message and stack payloads forwarded by
// the renderer so a noisy page cannot inflate log records without limit.
const maxFrontendErrorDetail = 8000

func clipFrontendErrorDetail(s string) string {
	if len(s) > maxFrontendErrorDetail {
		return s[:maxFrontendErrorDetail]
	}
	return s
}

// escapeLogLine keeps a renderer-supplied message on one physical log
// line. console.error and stack payloads carry newlines, and an
// unescaped record breaks every line-oriented reader of the file
// (grep/awk, Loki, the diagnostics log view).
func escapeLogLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", `\r\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

// ReportFrontendError records an uncaught renderer-side error (window error,
// unhandled rejection, React render crash, or console.error) through the OTel
// logger so frontend failures land in the same log file and OTLP sinks as Go
// diagnostics.
func (b *Diagnostics) ReportFrontendError(source, message, stack string) {
	flowtelemetry.Error(b.core.Shell.Context(), "frontend: "+source,
		log.String("message", escapeLogLine(clipFrontendErrorDetail(message))),
		log.String("stack", escapeLogLine(clipFrontendErrorDetail(stack))))
}
