// Package desktop hosts the opencraft desktop application: a Wails
// shell whose Go bindings drive the assembled flowcraft runtime and
// whose event bridge pushes runtime streams into the frontend. The
// package replaces the TUI/CLI entrypoints: the binary now starts the
// window, first-run onboarding lives in the GUI, and the execd child
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

	"github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/agents"
	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/runtime"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/setup"
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

	bridge *Bridge
	otelShutdown func(context.Context) error

	ctrl     *runtime.Controller
	broker   *runtime.Broker
	sessions *ocsessions.Store
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
		otelShutdown: shutdown,
	}, nil
}

// Startup is called by Wails once the window context exists.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.bridge.SetContext(ctx)
	if err := a.rebuild(); err != nil {
		a.bridge.Emit("fatal", map[string]any{"error": err.Error()})
	}
}

// Shutdown tears down the runtime when the window closes.
func (a *App) Shutdown(ctx context.Context) {
	a.closeRuntime()
	if a.otelShutdown != nil {
		flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = a.otelShutdown(flushCtx)
	}
}

// rebuild loads the user configuration layer and assembles a fresh
// runtime. When inference wiring is missing it emits the onboarding
// event instead; SaveSetup writes the config and calls rebuild again.
func (a *App) rebuild() error {
	a.closeRuntime()

	needed, err := setup.Needed(a.userDir)
	if err != nil {
		return fmt.Errorf("desktop: check config: %w", err)
	}
	if needed {
		a.bridge.Emit("onboarding_required", a.status(false))
		return nil
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	mgr, err := config.Open(config.Options{
		WorkDir: a.workDir,
		UserDir: a.userDir,
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
		app.WithUsageObserver(a.bridge.Usage))
	if err != nil {
		return fmt.Errorf("desktop: assemble runtime: %w", err)
	}
	ctrl := runtime.NewController(rt)
	broker := ctrl.Broker(a.bridge)
	if err := broker.Attach(ctx); err != nil {
		_ = ctrl.Close()
		return fmt.Errorf("desktop: attach broker: %w", err)
	}

	userData, err := config.UserDataDir()
	if err != nil {
		broker.Close()
		_ = ctrl.Close()
		return fmt.Errorf("desktop: user data dir: %w", err)
	}
	store, err := ocsessions.New(filepath.Join(userData, "sessions"), 40)
	if err != nil {
		broker.Close()
		_ = ctrl.Close()
		return fmt.Errorf("desktop: session store: %w", err)
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
	st := ConfigStatus{
		Needed:       !configured,
		DefaultModel: config.DefaultModel(a.userDir),
		WorkDir:      a.workDir,
		UserDir:      a.userDir,
		Version:      app.ServiceVersion,
	}
	a.mu.Lock()
	if a.agents != nil {
		st.Agents = len(a.agents.List())
	}
	a.mu.Unlock()
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
