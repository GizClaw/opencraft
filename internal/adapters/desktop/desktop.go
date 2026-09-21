package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"

	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/bindings"
	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	petfeed "github.com/GizClaw/opencraft/internal/adapters/desktop/pet"
	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/execd"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	octelemetry "github.com/GizClaw/opencraft/internal/capabilities/telemetry"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/envpath"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// Options configures the desktop application.
type Options struct {
	WorkDir string
	UserDir string
	DataDir string
}

// Desktop is the desktop composition root. It is not a Wails binding object;
// RegisterServices exposes the per-domain API objects as Wails v3 services.
type Desktop struct {
	core               *core.Core
	notifications      *notifications.NotificationService
	telemetryPipeline  *octelemetry.Pipeline
	execPool           *execd.Pool
	runtimeMetricsStop chan struct{}
	runtimeMetricsDone chan struct{}
	// media streams workspace files (generated videos) to the webview
	// over a loopback listener; nil disables inline media playback.
	media *mediaServer

	petMu     sync.Mutex
	petWindow *application.WebviewWindow
	petStop   chan struct{}
	petCtx    context.Context
	// petX/petY track the window position so drag input from the pet
	// surface and the autonomous rover share one source of truth.
	petX, petY     int
	petManualUntil time.Time
	petReady       bool
	petDirector    *petfeed.PetDirector
	// Drag gesture state: the surface marks begin/end, and each
	// SetPosition step feeds the walk cycle and the turning direction.
	petDragging    bool
	petDragFacing  string
	petDragDx      int
	petDragMovedAt time.Time
	// petGeometry is where the renderer drew the character inside the
	// pet window; the placement maths anchors on it instead of on the
	// window rectangle. It stays at the shipped layout until the
	// surface reports its own measurement.
	petGeometry petfeed.WindowGeometry
	petDebug    petfeed.MindDebug
}

// New resolves the user data/config directories and builds the core
// service composition.
func New(opts Options) (*Desktop, error) {
	if opts.DataDir == "" {
		dir, err := config.UserDataDir()
		if err != nil {
			return nil, err
		}
		opts.DataDir = dir
	}
	if opts.UserDir == "" {
		dir, err := config.UserConfigDir()
		if err != nil {
			return nil, err
		}
		opts.UserDir = dir
	}
	if _, err := config.EnsureUserConfig(); err != nil {
		return nil, err
	}
	c := core.NewCore(opts.UserDir, opts.DataDir, opts.WorkDir)
	c.Prompt.SetNotifier(c.Shell.Emit)
	c.Prompt.SetRunConvResolver(c.Conversation.ConversationForRun)
	c.Runtime.Manager().SetUsageObserver(func(_ context.Context, usage inference.Usage) {
		c.Shell.Emit("usage", core.NewUsageEvent(usage))
	})
	c.Runtime.SetHostConfigurator(func(h *host.Host) {
		h.SetArtifactObserver(func(ctx context.Context, path string, data []byte) {
			if h != c.Runtime.Current() {
				return
			}
			info, ok := agent.RunInfoFromContext(ctx)
			if !ok || info.ConversationID == "" {
				return
			}
			c.Shell.Emit("artifact", map[string]any{
				"conversation_id": info.ConversationID,
				"path":            path,
				"bytes":           len(data),
			})
		})
		h.SetSessionUpdated(func(_ context.Context, contextID string) {
			if h == c.Runtime.Current() {
				c.Shell.Emit("session_updated", map[string]string{"id": contextID})
			}
		})
	})
	c.SetWorkDir(c.InitialWorkDir(opts.WorkDir))
	pipeline, err := initTelemetry(opts.DataDir)
	if err != nil {
		// Telemetry is best-effort for the desktop app: a failed
		// pipeline must not block the window.
		fmt.Fprintf(os.Stderr, "opencraft: telemetry: %v\n", err)
		pipeline = nil
	}
	// The plugin telemetry handler is wired by the composition root and
	// resolves this pipeline per call, so it can be attached here.
	c.Telemetry = pipeline
	// Resolve PATH once the log sink exists (the line below is the record
	// of what the app runs with) but before anything can spawn: a
	// Finder/Dock launch inherits launchd's minimal PATH, so without this
	// the MCP servers, the commands agents run and the app's own gh/git
	// lookups cannot see Homebrew or other user-local installs. The merge
	// rules live in foundation/utils/envpath.
	resolveProcessPath(c)
	d := &Desktop{
		core: c,
		// Windows toast attribution keys off application.Options.Name
		// ("OpenCraft"); the NSIS installer stamps the same AppUserModelID
		// onto the shortcuts it creates. Keep main.go's Options.Name and
		// build/config.yml's productName in sync with that value.
		notifications:     notifications.New(),
		telemetryPipeline: pipeline,
	}
	// The exec supervisor pool is process-wide: it pre-warms children
	// and caps how many workspaces hold one at a time. Settings come
	// from desktop.json (Settings > Diagnostics).
	execPool := execd.NewPool(c.Shell.ExecPool())
	execd.SetDefaultPool(execPool)
	d.execPool = execPool
	c.Shell.SetNotificationSink(d.handleDesktopNotification)
	// Media playback needs http(s): the webview loads the frontend over
	// wails:// (or the Vite dev server), which WebKit/AVFoundation
	// cannot play. Streaming is an enhancement, so a failure logs and
	// leaves video previews on the system-player fallback.
	if media, err := newMediaServer(c.ActiveWorkDir); err != nil {
		telemetry.WarnErr(c.Shell.Context(),
			"desktop: media streaming disabled", err)
	} else {
		d.media = media
	}
	return d, nil
}

// resolveProcessPath installs the merged PATH and records it. Resolution
// is best-effort: a failure leaves the inherited PATH in place and must
// never block the window.
func resolveProcessPath(c *core.Core) {
	ctx := c.Shell.Context()
	resolved, err := envpath.Install(envpath.Options{
		Prepend: c.Shell.PathPrepend(),
	})
	if err != nil {
		telemetry.WarnErr(ctx, "envpath: resolve process PATH failed", err)
		return
	}
	c.SetPathReport(resolved)
	telemetry.Info(ctx, "envpath: process PATH resolved",
		otellog.String("path", resolved.Path),
		otellog.Bool("changed", resolved.Changed),
		otellog.String("prepend", strings.Join(
			resolved.Dirs(envpath.SourcePrepend), ", ")),
		otellog.String("appended", strings.Join(
			resolved.Dirs(envpath.SourceCandidate), ", ")),
		otellog.String("missing", strings.Join(resolved.Missing, ", ")),
		otellog.String("rejected", strings.Join(resolved.Rejected, ", ")),
	)
}

// initTelemetry wires the OTel pipelines (rotating log file under
// ~/.opencraft/logs plus optional OTLP export) and returns their owner.
// The pipeline keeps the file sink active when a capability plugin
// swaps the export target at runtime.
func initTelemetry(dataDir string) (*octelemetry.Pipeline, error) {
	logPath := filepath.Join(dataDir, "logs", "opencraft.log")
	otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	otelInsecure := false
	if v := os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"opencraft: telemetry: invalid OTEL_EXPORTER_OTLP_INSECURE %q: %v\n",
				v, err)
		} else {
			otelInsecure = parsed
		}
	}
	return octelemetry.Start(context.Background(), octelemetry.TelemetryOptions{
		OTLPEndpoint: otelEndpoint,
		OTLPInsecure: otelInsecure,
		// Header values are credentials for the operator-configured
		// collector; they are never logged or persisted.
		OTLPHeaders: otelHeadersFromEnv(),
		LogFile:     logPath,
	})
}

// otelHeadersFromEnv reads the standard OTEL_EXPORTER_OTLP_HEADERS
// variable ("key=value,key2=value2"). Values may be URL-encoded, which
// is how the OTel specification transports characters like "," or "="
// inside a value.
func otelHeadersFromEnv() map[string]string {
	raw := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"))
	if raw == "" {
		return nil
	}
	headers := map[string]string{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, value, ok := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			fmt.Fprintf(os.Stderr,
				"opencraft: telemetry: invalid OTEL_EXPORTER_OTLP_HEADERS entry %q\n",
				entry)
			continue
		}
		// Percent-decoding only: url.QueryUnescape would also turn "+"
		// into a space, which corrupts base64/Bearer credentials. This
		// matches the OTel Go SDK's own header parsing.
		if decoded, err := url.PathUnescape(strings.TrimSpace(value)); err == nil {
			value = decoded
		} else {
			value = strings.TrimSpace(value)
		}
		headers[name] = value
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// Startup wires the application context into the core shell.
func (d *Desktop) Startup(ctx context.Context) {
	started := time.Now()
	defer func() {
		durationMs := time.Since(started).Milliseconds()
		octelemetry.SampleHistogram(
			ctx, "desktop.startup_ms", "ms", float64(durationMs))
		if mgr := d.core.Runtime.Manager(); mgr != nil {
			mgr.RecordMetric(ctx, "desktop.startup_ms",
				float64(durationMs), nil)
		}
		telemetry.Info(ctx, fmt.Sprintf(
			"desktop: startup completed in %d ms", durationMs))
	}()
	d.core.Shell.SetContext(ctx)
	d.core.Shell.SetScheduledTasksChecker(d.hasScheduledTasks)
	d.petMu.Lock()
	d.petCtx = ctx
	d.petMu.Unlock()
	d.core.Shell.SetPetsChangedListener(d.onPetsChanged)
	if err := d.core.Runtime.OpenUserDB(ctx); err != nil {
		telemetry.WarnErr(ctx, "desktop: open user db failed", err)
	} else {
		d.startRuntimeMetrics()
		d.startAutomations(ctx)
	}
	if runtime.GOOS == "darwin" {
		// macOS shows an authorization prompt once; ask after every
		// service has started so the request cannot race startup.
		time.AfterFunc(2*time.Second, func() {
			if _, err := d.notifications.RequestNotificationAuthorization(); err != nil {
				telemetry.WarnErr(context.Background(),
					"desktop: request notification authorization failed", err)
			}
		})
	}
	if err := d.core.RebuildRuntime(
		host.WithAssemblyReason(ctx, host.ReasonStartup)); err != nil {
		d.core.Shell.Emit("fatal", map[string]any{"error": err.Error()})
	}
	d.ensureAssistantPet(ctx)
}

// EmitUI is the single UI-event entry used by the v3 entry point outside the
// domain services; everything else already routes through core.Shell.Emit.
func (d *Desktop) EmitUI(typ string, data any) {
	d.core.Shell.Emit(typ, data)
}

// QuitAllowed is the v3 quit gate: it returns true only when quitting may
// proceed immediately and opens the async confirmation dialog otherwise.
func (d *Desktop) QuitAllowed() bool {
	return d.core.Shell.ShouldQuit()
}

// QuitRequested reports whether a quit flow is already under way (dialog
// pending or confirmed). Window close events fired during that flow must not
// trigger a second quit request.
func (d *Desktop) QuitRequested() bool {
	return d.core.Shell.QuitRequested()
}

// RequestQuit funnels every explicit quit (tray, UI service, Cmd+Q) through
// the confirmation gate.
func (d *Desktop) RequestQuit() {
	if d.QuitAllowed() {
		d.core.Shell.QuitApplication()
	}
}

// CloseRequested funnels native window closes through the core close gate:
// close-to-tray hides, a real quit runs the confirmation dialog.
func (d *Desktop) CloseRequested() bool {
	return d.core.Shell.CloseRequested(d.core.Shell.Context())
}

// SetDialogIcon forwards the app icon to the core shell for native dialogs.
func (d *Desktop) SetDialogIcon(icon []byte) {
	d.core.Shell.SetDialogIcon(icon)
}

// hasScheduledTasks reports whether quitting would stop an enabled
// scheduled task. It is the native quit funnel's condition: no
// scheduler (user DB failed to open) means nothing can run, so exit
// needs no confirmation; a query failure is treated conservatively as
// "tasks exist" so users are never told the wrong thing before quit.
func (d *Desktop) hasScheduledTasks(ctx context.Context) bool {
	store := d.core.Runtime.Automations()
	if store == nil {
		return false
	}
	has, err := store.HasEnabled(ctx)
	if err != nil {
		telemetry.WarnErr(ctx, "desktop: query scheduled tasks failed", err)
		return true
	}
	return has
}

// Shutdown releases runtime-owned resources. Runtime service teardown
// is added as the runtime domain migrates.
func (d *Desktop) Shutdown(ctx context.Context) {
	d.stopPet()
	d.stopRuntimeMetrics()
	d.media.Close()
	if mgr := d.core.Runtime.AutomationManager(); mgr != nil {
		mgr.Stop()
	}
	// Detach the notification sink before the runtime closes. A turn
	// that finishes while shutdown is in flight still raises turn_end,
	// and resolving its session title against a closed store would log
	// "database is closed" on every exit.
	d.core.Shell.SetNotificationSink(nil)
	// A window that is going away never receives a half-flushed stream.
	d.core.Shell.ClearPendingStreams()
	d.core.Runtime.Close()
	d.core.Plugin.Close()
	if d.execPool != nil {
		// Runtimes closed first: their runners released every lease back
		// to the pool, and only then is there nothing left to serve.
		execd.SetDefaultPool(nil)
		d.execPool.Close()
	}
	if d.telemetryPipeline != nil {
		// The Wails shutdown context may already be canceled by the
		// time this runs; derive the flush deadline from a fresh
		// context so telemetry still gets its full drain window.
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.telemetryPipeline.Shutdown(flushCtx); err != nil {
			telemetry.WarnErr(context.Background(),
				"desktop: telemetry shutdown failed", err)
		}
	}
}

func (d *Desktop) startAutomations(ctx context.Context) {
	store := d.core.Runtime.Automations()
	if store == nil {
		return
	}
	mgr, err := automations.NewManager(store, automations.ManagerOptions{
		Run:    d.runAutomation,
		Window: 2 * time.Minute,
		Limit:  4,
		OnChange: func() {
			d.core.Shell.Emit("automation_changed", map[string]any{})
		},
		OnRun: func(run automations.Run) {
			d.core.Shell.Emit("automation_run", bindings.ToAutomationRunDTO(run))
		},
	})
	if err != nil {
		return
	}
	d.core.Runtime.SetAutomationManager(mgr)
	mgr.Start()
}

func (d *Desktop) runAutomation(
	ctx context.Context, task automations.Task,
) (automations.RunResult, error) {
	mode := sessions.Mode(task.Mode)
	if mode == "" {
		mode = sessions.ModeWorkspace
	}
	current := d.core.ActiveWorkDir() != "" &&
		filepath.Clean(d.core.ActiveWorkDir()) == filepath.Clean(task.Workspace)
	h, err := d.core.Runtime.AcquireBackground(
		host.WithAssemblyReason(ctx, host.ReasonAutomation),
		task.Workspace, interact.Auto{})
	if err != nil {
		return automations.RunResult{Status: automations.RunFailed}, err
	}
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:   message.NewTextMessage(message.RoleUser, task.Prompt),
		ContextID: task.ConversationID,
		Mode:      mode,
		Think:     task.Think,
		Model:     task.Model,
		Backend:   interact.Auto{},
	})
	if err != nil {
		return automations.RunResult{Status: automations.RunFailed}, err
	}
	runID := run.RunID()
	contextID := run.ContextID()
	if current {
		d.core.Shell.Emit("automation_run_started", map[string]any{
			"run_id":          runID,
			"conversation_id": contextID,
		})
	}
	// The manager's run context carries the task timeout; WaitBounded
	// turns that deadline into a cancel and waits for the settle, so
	// the archive write, the memory commit and the slot release still
	// happen before this returns.
	res, waitErr := run.WaitBounded(ctx)
	finishedAt, durationMs := run.FinishedTiming()
	// One shared mapping classifies the settled pair (a completed status
	// and no wait error = the run completed; anything else failed, for
	// the manager to rewrite to a timeout or a canceled when it caused
	// the stop itself).
	outcome := automations.ClassifySettledRun(contextID, runID, res, waitErr)
	result := outcome.Result
	status, errText := outcome.Status, outcome.ErrorText
	// A run stopped from the panel is not a failure: the record says
	// canceled (the manager stamps the same status) and the failure
	// notification policy stays quiet, because the user just asked for
	// the stop.
	if errors.Is(ctx.Err(), context.Canceled) &&
		result.Status != automations.RunCompleted {
		result.Status = automations.RunCanceled
		result.Error = ""
	}
	output := automationOutput(res)
	notify := !suppressAutomationNotify(task, result.Status, result.Error)
	if current {
		requestID, responseID := run.FinishedIDs()
		end := core.NewTurnEnd(
			runID, contextID, string(status), errText,
			requestID, responseID, output,
			finishedAt, durationMs, res,
		)
		class := host.ClassifyRunError(resErr(res), waitErr)
		end.InterruptCause = class.InterruptCause
		end.ErrorKind = class.ErrorKind
		end.AgentID = core.AssistantAgentID
		end.Notify = &notify
		d.core.Shell.Emit("turn_end", end)
	} else if notify {
		// No UI consumer for this payload: the automation panels refresh
		// from automation_run / automation_changed, so the banner is
		// raised through the notification path instead of the event bus.
		d.core.Shell.Notify(core.NotifyAutomation, map[string]any{
			"task_id": task.ID,
			"name":    task.Name,
			"status":  string(result.Status),
			"error":   result.Error,
			"output":  output,
		})
	}
	if waitErr != nil {
		return result, waitErr
	}
	return result, nil
}

// automationOutput returns the bounded text of the run's final
// assistant message for notifications outside the open workspace.
// resErr is the error the engine reported, or nil when the run
// produced a result.
func resErr(res *agent.Result) error {
	if res == nil {
		return nil
	}
	return res.Err
}

func automationOutput(res *agent.Result) string {
	if res == nil {
		return ""
	}
	for i := len(res.Messages) - 1; i >= 0; i-- {
		if res.Messages[i].Role != message.RoleAssistant {
			continue
		}
		text := strings.TrimSpace(res.Messages[i].Content.Text())
		if text == "" {
			continue
		}
		if len(text) > 8000 {
			text = text[len(text)-8000:]
		}
		return text
	}
	return ""
}

// suppressAutomationNotify applies the task's notification policy.
func suppressAutomationNotify(
	task automations.Task, status automations.RunStatus, errorText string,
) bool {
	switch task.Notify {
	case automations.NotifyNever:
		return true
	case automations.NotifyFailed:
		return status != automations.RunFailed && errorText == ""
	default:
		return false
	}
}

// RegisterServices registers every domain binding as a Wails v3 service.
// Each call is written out explicitly so the v3 binding generator can infer
// concrete service types from NewService.
func (d *Desktop) RegisterServices(app *application.App) {
	reg := func(s application.Service) {
		app.RegisterService(s)
	}
	reg(application.NewService(bindings.NewLifecycle(d.core)))
	reg(application.NewService(bindings.NewConfig(d.core)))
	reg(application.NewService(bindings.NewWorkspace(d.core)))
	reg(application.NewService(bindings.NewConversationBinding(d.core)))
	reg(application.NewService(bindings.NewSessionBinding(d.core)))
	reg(application.NewService(bindings.NewAgentBinding(d.core)))
	files := bindings.NewFileBinding(d.core)
	files.SetMediaURL(d.mediaURL)
	reg(application.NewService(files))
	reg(application.NewService(bindings.NewGitBinding(d.core)))
	reg(application.NewService(bindings.NewPullRequestsBinding(d.core)))
	reg(application.NewService(bindings.NewSettingsBinding(d.core)))
	reg(application.NewService(bindings.NewDiagnosticsBinding(d.core)))
	reg(application.NewService(bindings.NewPluginBinding(d.core)))
	reg(application.NewService(bindings.NewSecretBinding(d.core)))
	reg(application.NewService(bindings.NewAutomationBinding(d.core)))
	reg(application.NewService(bindings.NewPetBinding(d.core)))
	reg(application.NewService(d.notifications))
}

// mediaURL builds the loopback stream URL for one workspace-relative
// file, or reports why streaming is unavailable.
func (d *Desktop) mediaURL(rel string) (string, error) {
	if d.media == nil {
		return "", errors.New("desktop: media streaming is unavailable")
	}
	return d.media.URL(rel)
}
