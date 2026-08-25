// Package desktop hosts the opencraft desktop application: a Wails
// shell whose Go bindings drive the assembled flowcraft runtime and
// whose event bridge pushes runtime streams into the frontend. The
// package replaces the TUI/CLI entrypoints: the binary now starts the
// window, configuration lives in the GUI, and the execd child
// mode is handled by the root main package before Wails starts.
package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/agents"
	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/runtime"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/usage"
)

// Options configures the desktop application.
type Options struct {
	// WorkDir is the workspace the app operates on (project config
	// discovery + the file panel). Empty uses the current working
	// directory, falling back to the user's home when the app was
	// launched from Finder (cwd "/").
	WorkDir string
	// UserDir overrides ~/.opencraft/config (tests).
	UserDir string
}

// App is the Wails-bound application root. Exported methods on App
// become JS bindings; everything else is internal state.
type App struct {
	ctx context.Context

	mu      sync.Mutex
	workDir string
	userDir string

	bridge       *Bridge
	otelShutdown func(context.Context) error

	ctrl     *runtime.Controller
	broker   *runtime.Broker
	sessions *ocsessions.Store
	usage    *usage.Store
	agents   *agents.Lifecycle
	turns    map[string]*session.Turn

	// conversationID is the stable session context for the current
	// conversation. Every turn in the conversation reuses it so
	// history accumulates and the sandbox permission mode applies to
	// the whole conversation; NewChat mints a fresh one.
	conversationID string
	// mode is the sandbox permission mode applied to new turns
	// (defaults to workspace; yolo disables the sandbox).
	mode ocsessions.Mode
	// think is the reasoning effort applied to new turns
	// (low/medium/high), persisted per conversation.
	think string
	// model is the per-conversation model hint applied to new turns
	// ("provider/name", or "" for the default routing policy),
	// persisted per conversation.
	model string
	// runConvs maps active run ids to their conversation, so stream
	// events and usage can be routed with parallel turns.
	runConvs map[string]string
	// convRuns retains every run id minted per conversation for the
	// app lifetime, so delegation cards (which persist the caller run
	// id) can be attributed back to the conversation that spawned them
	// even after the calling turn ended.
	convRuns map[string]map[string]bool
	// runUsage accumulates inference usage per conversation while its
	// turn is active; it is recorded into the session store at turn
	// end.
	runUsage map[string]ocsessions.Usage
	// titling tracks conversations whose auto-title generation is in
	// flight, so parallel turn endings generate once per conversation.
	titling map[string]bool
}

// New creates the application shell. Runtime assembly is deferred to
// Startup so the Wails context is available for event emission.
func New(opts Options) (*App, error) {
	workDir := opts.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	if workDir == "/" {
		if home, err := os.UserHomeDir(); err == nil {
			workDir = home
		}
	}
	userDir := opts.UserDir
	if userDir == "" {
		dir, err := config.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("desktop: user config dir: %w", err)
		}
		userDir = dir
	}
	if _, err := config.EnsureUserConfig(); err != nil {
		return nil, fmt.Errorf("desktop: seed config: %w", err)
	}
	shutdown, err := initTelemetry()
	if err != nil {
		// Telemetry is best-effort for the desktop app: a failed
		// pipeline must not block the window.
		fmt.Fprintf(os.Stderr, "opencraft: telemetry: %v\n", err)
		shutdown = nil
	}
	return &App{
		workDir:        workDir,
		userDir:        userDir,
		bridge:         NewBridge(),
		turns:          make(map[string]*session.Turn),
		conversationID: ocsessions.NewID(),
		mode:           ocsessions.ModeWorkspace,
		think:          string(ocsessions.ThinkMedium),
		model:          "",
		runConvs:       make(map[string]string),
		convRuns:       make(map[string]map[string]bool),
		runUsage:       make(map[string]ocsessions.Usage),
		titling:        make(map[string]bool),
		otelShutdown:   shutdown,
	}, nil
}

// Startup is called by Wails once the window context exists.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.bridge.SetContext(ctx)
	// Route stream/interact events to their owning conversation so a
	// frontend reload can recover mid-run routing; delegated subagent
	// runs resolve to "" and stay out of the chat.
	a.bridge.SetRunConvResolver(func(runID string) string {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.runConvs[runID]
	})
	if err := a.rebuild(); err != nil {
		a.bridge.Emit("fatal", map[string]any{"error": err.Error()})
	}
}

// Shutdown tears down the runtime when the window closes.
func (a *App) Shutdown(ctx context.Context) {
	a.closeRuntime()
	a.mu.Lock()
	if a.usage != nil {
		_ = a.usage.Close()
		a.usage = nil
	}
	a.mu.Unlock()
	if a.otelShutdown != nil {
		flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = a.otelShutdown(flushCtx)
	}
}

// rebuild loads the user configuration layer and assembles a fresh
// runtime. Inference wiring is not required to start: an unconfigured
// install builds with the embedded router shell and the UI guides the
// user to the settings page.
func (a *App) rebuild() error {
	a.closeRuntime()

	a.mu.Lock()
	wd := a.workDir
	ud := a.userDir
	a.mu.Unlock()
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// The user-level usage database is workspace-independent and opened
	// once per app run.
	a.mu.Lock()
	if a.usage == nil {
		if dataDir, err := config.UserDataDir(); err == nil {
			if store, err := usage.Open(filepath.Join(dataDir, "user.db")); err == nil {
				a.usage = store
			}
		}
	}
	a.mu.Unlock()
	mgr, err := config.Open(config.Options{
		WorkDir: wd,
		UserDir: ud,
	})
	if err != nil {
		return fmt.Errorf("desktop: open config: %w", err)
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		return fmt.Errorf("desktop: load config: %w", err)
	}
	rt, err := app.BuildRuntime(ctx, view.Document,
		app.WithConfigBase(mgr.UserDir()),
		app.WithWorkBase(wd),
		app.WithUsageObserver(a.onUsage))
	if err != nil {
		return fmt.Errorf("desktop: assemble runtime: %w", err)
	}
	ctrl := runtime.NewController(rt)
	broker := ctrl.Broker(a.bridge)
	if err := broker.Attach(ctx); err != nil {
		_ = ctrl.Close()
		return fmt.Errorf("desktop: attach broker: %w", err)
	}

	// Prefer the runtime's session store resource: the memory hook
	// and the sandbox share it, so permissions (YOLO), history, and
	// the sessions list all read the same data. A private store is
	// only a fallback for runtimes assembled without the resource.
	var store *ocsessions.Store
	if value, ok := rt.Resource("sessions"); ok {
		if svc, ok := value.(*ocsessions.Store); ok {
			store = svc
		}
	}
	if store == nil {
		userData, err := config.UserDataDir()
		if err != nil {
			broker.Close()
			_ = ctrl.Close()
			return fmt.Errorf("desktop: user data dir: %w", err)
		}
		store, err = ocsessions.New(filepath.Join(userData, "sessions"), 40)
		if err != nil {
			broker.Close()
			_ = ctrl.Close()
			return fmt.Errorf("desktop: session store: %w", err)
		}
	}

	var lifecycle *agents.Lifecycle
	if value, ok := rt.Resource("agentlifecycle"); ok {
		if svc, ok := value.(*agents.Lifecycle); ok {
			lifecycle = svc
		}
	}

	a.mu.Lock()
	a.ctrl = ctrl
	a.broker = broker
	a.sessions = store
	a.agents = lifecycle
	a.mu.Unlock()

	// Remember this workspace in the history (best-effort) once the
	// runtime is healthy, so every successful open/switch lands here.
	a.recordWorkspace(wd)

	a.bridge.Emit("ready", a.status(true))
	return nil
}

func (a *App) closeRuntime() {
	a.mu.Lock()
	broker := a.broker
	ctrl := a.ctrl
	store := a.sessions
	a.broker = nil
	a.ctrl = nil
	a.sessions = nil
	a.turns = make(map[string]*session.Turn)
	a.runConvs = make(map[string]string)
	a.convRuns = make(map[string]map[string]bool)
	a.runUsage = make(map[string]ocsessions.Usage)
	a.mu.Unlock()

	if broker != nil {
		broker.Close()
	}
	if ctrl != nil {
		_ = ctrl.Close()
	}
	if store != nil {
		_ = store.Close()
	}
}

func (a *App) status(configured bool) ConfigStatus {
	a.mu.Lock()
	wd := a.workDir
	ud := a.userDir
	agents := 0
	if a.agents != nil {
		agents = len(a.agents.List())
	}
	a.mu.Unlock()
	st := ConfigStatus{
		Needed:       !configured,
		DefaultModel: config.DefaultModel(ud),
		WorkDir:      wd,
		UserDir:      ud,
		Version:      app.ServiceVersion,
		Agents:       agents,
	}
	return st
}

// appContext returns the Wails application context, falling back to a
// background context before Startup ran.
func (a *App) appContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// onUsage forwards one usage report to the bridge and accumulates it
// for the owning run's session record. It runs on the engine's
// goroutine and must be non-blocking. Attribution uses the run id from
// the report context (parallel turns each carry their own RunInfo);
// the bridge's last-stream-run fallback covers reports without one.
func (a *App) onUsage(ctx context.Context, u inference.Usage) {
	a.bridge.Usage(u)
	runID := ""
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		runID = info.RunID
	}
	if runID == "" {
		runID = a.bridge.LastStreamRun()
	}
	a.mu.Lock()
	conv := a.runConvs[runID]
	if conv == "" {
		a.mu.Unlock()
		return
	}
	acc := a.runUsage[runID]
	acc.TotalTokens += u.TotalTokens
	acc.InputTokens += u.InputTokens
	acc.OutputTokens += u.OutputTokens
	if u.Model.ID.Provider != "" && u.Model.ID.Name != "" {
		acc.Model = u.Model.ID.Provider + "/" + u.Model.ID.Name
	}
	if u.Output.ReasoningTokens != nil {
		acc.ReasoningTokens += *u.Output.ReasoningTokens
	}
	if u.Input.CacheReadTokens != nil {
		acc.CacheReadTokens += *u.Input.CacheReadTokens
	}
	if u.Input.CacheWriteTokens != nil {
		acc.CacheWriteTokens += *u.Input.CacheWriteTokens
	}
	a.runUsage[runID] = acc
	a.mu.Unlock()
}

// initTelemetry wires the OTel pipelines (rotating log file under
// ~/.opencraft/logs plus optional OTLP export) and returns the
// flush/shutdown function.
func initTelemetry() (func(context.Context) error, error) {
	dataDir, err := config.UserDataDir()
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(dataDir, "logs", "opencraft.log")
	otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	otelInsecure := false
	if v := os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"); v != "" {
		otelInsecure, _ = strconv.ParseBool(v)
	}
	return app.InitOtel(context.Background(), app.TelemetryOptions{
		OTLPEndpoint: otelEndpoint,
		OTLPInsecure: otelInsecure,
		LogFile:      logPath,
	})
}
