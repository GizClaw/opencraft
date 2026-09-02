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
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/agents"
	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/automations"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/plugins"
	pluginruntime "github.com/GizClaw/opencraft/internal/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/rollout"
	"github.com/GizClaw/opencraft/internal/runtime"
	"github.com/GizClaw/opencraft/internal/secrets"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/undo"
	"github.com/GizClaw/opencraft/internal/usage"
	"github.com/GizClaw/opencraft/internal/userdb"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Options configures the desktop application.
type Options struct {
	// WorkDir is the workspace the app operates on (project config
	// discovery + the file panel). Empty restores the most recently
	// opened workspace; a fresh install with no history starts with no
	// workspace selected (the UI shows the welcome screen / workspace
	// picker instead of the chat).
	WorkDir string
	// UserDir overrides ~/.opencraft/config (tests).
	UserDir string
	// DataDir overrides the user data root ~/.opencraft (tests); the
	// credential store lives under <dataDir>/keyring on Linux.
	DataDir string
	// TrayIcon is the system tray / menu bar icon (PNG). macOS renders
	// it as a template image; nil skips the icon (tests).
	TrayIcon []byte
	// TrayIconTemplate is the monochrome macOS menu bar glyph (PNG,
	// black + alpha). When set, macOS uses it as the template icon while
	// Windows/Linux keep the full-colour TrayIcon. Nil falls back to
	// TrayIcon (tests).
	TrayIconTemplate []byte
}

// App is the Wails-bound application root. Exported methods on App
// become JS bindings; everything else is internal state.
type App struct {
	ctx context.Context

	mu      sync.Mutex
	workDir string
	userDir string
	// pluginDir is the frontend plugin root (<dataDir>/plugins); set by
	// New and overridable in tests.
	pluginDir string
	// plugins / kv / auth are the plugin subsystem entry points.
	plugins *plugins.Store
	kv      *plugins.KVStore
	// cap hosts subprocess capability plugins (e.g. the SSO auth
	// protocol). Lazily started on first PluginInvoke.
	cap *pluginruntime.Manager

	bridge       *Bridge
	otelShutdown func(context.Context) error

	ctrl            *runtime.Controller
	broker          *runtime.Broker
	sessions        *ocsessions.Store
	usage           *usage.Store
	userDB          *userdb.DB
	automationStore *automations.Store
	automations     *automations.Manager
	// backgroundHosts pools one background runtime per workspace for
	// automation runs whose target workspace is not the currently open
	// one (and for the open one too: automation never reuses the UI
	// runtime).
	backgroundHosts map[string]*backgroundHost
	agents          *agents.Lifecycle
	undo            *undo.Store
	secrets         *secrets.Manager
	turns           map[string]*session.Turn
	// pendingRebuild defers any runtime rebuild (settings, MCP,
	// plugins, workspace switch) until no turn is running, so
	// unattended automation runs survive configuration changes.
	pendingRebuild bool
	// pendingWorkDir carries a workspace switch requested while a run
	// was active; it is applied once the last turn ends.
	pendingWorkDir string

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
	// preTurnSnap holds each running turn's pre-state so waitTurn can
	// pair it with the post-state and record an undo entry.
	preTurnSnap map[string][]undo.FileState
	// preTurnManifest holds each running turn's pre-turn workspace
	// manifest so waitTurn can reconcile exec-produced document files
	// (git-free) and merge them into the archived turn.
	preTurnManifest map[string]map[string]fileStat
	// rollouts maps conversation ids to their JSONL event recorder.
	rollouts map[string]*rollout.Recorder
	// rolloutBufs buffer streamed text/reasoning per run until the
	// finish delta, so assistant items are recorded whole.
	rolloutBufs map[string]*rolloutBuffer

	// closeToTray persists the close behaviour ("hide to tray" vs
	// "quit"). It is read by the Wails close hook on the UI thread.
	closeToTray bool
	// quitting is set when the user chooses Quit from the tray menu, so
	// OnBeforeClose lets Wails terminate instead of hiding again.
	quitting bool
	// trayIcon is the system tray icon bytes (nil in tests).
	trayIcon []byte
	// trayIconTemplate is the macOS menu bar glyph bytes (nil in tests).
	trayIconTemplate []byte
	// trayEnd is the systray external-loop teardown function (nil in
	// tests); set by startTray, consumed by stopTray.
	trayEnd func()
}

// rolloutBuffer accumulates one run's streamed assistant parts.
type rolloutBuffer struct {
	text      strings.Builder
	reasoning strings.Builder
}

// New creates the application shell. Runtime assembly is deferred to
// Startup so the Wails context is available for event emission.
func New(opts Options) (*App, error) {
	historyDir, err := workspaceHistoryDir()
	if err != nil {
		historyDir = ""
	}
	workDir := startupWorkDir(opts.WorkDir, historyDir)
	var undoStore *undo.Store
	if workDir != "" {
		undoStore = undo.New(workDir)
	}
	userDir := opts.UserDir
	if userDir == "" {
		dir, err := config.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("desktop: user config dir: %w", err)
		}
		userDir = dir
	}
	prefs, err := loadPrefs(opts.UserDir)
	if err != nil {
		// A preference file read failure must not block the window;
		// defaults apply and the next save rewrites the file.
		prefs = desktopPrefs{CloseToTray: true}
	}
	if _, err := config.EnsureUserConfig(); err != nil {
		return nil, fmt.Errorf("desktop: seed config: %w", err)
	}
	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir, _ = config.UserDataDir()
	}
	sec := secrets.NewManager(filepath.Join(dataDir, "keyring"), secrets.DefaultService)
	pluginDir := filepath.Join(dataDir, "plugins")
	shutdown, err := initTelemetry()
	if err != nil {
		// Telemetry is best-effort for the desktop app: a failed
		// pipeline must not block the window. The failure itself
		// cannot go through telemetry (it is what failed), so it is
		// reported on stderr instead.
		fmt.Fprintf(os.Stderr, "opencraft: telemetry: %v\n", err)
		shutdown = nil
	}
	a := &App{
		workDir:          workDir,
		userDir:          userDir,
		pluginDir:        pluginDir,
		plugins:          plugins.NewStore(pluginDir),
		kv:               plugins.NewKVStore(pluginDir),
		bridge:           NewBridge(),
		turns:            make(map[string]*session.Turn),
		conversationID:   ocsessions.NewID(),
		mode:             ocsessions.ModeWorkspace,
		think:            string(ocsessions.ThinkMedium),
		model:            "",
		runConvs:         make(map[string]string),
		convRuns:         make(map[string]map[string]bool),
		backgroundHosts:  make(map[string]*backgroundHost),
		runUsage:         make(map[string]ocsessions.Usage),
		titling:          make(map[string]bool),
		preTurnSnap:      make(map[string][]undo.FileState),
		preTurnManifest:  make(map[string]map[string]fileStat),
		undo:             undoStore,
		secrets:          sec,
		rollouts:         make(map[string]*rollout.Recorder),
		rolloutBufs:      make(map[string]*rolloutBuffer),
		otelShutdown:     shutdown,
		closeToTray:      prefs.CloseToTray,
		trayIcon:         opts.TrayIcon,
		trayIconTemplate: opts.TrayIconTemplate,
	}
	a.plugins.SetHostVersion(app.ServiceVersion)
	a.cap = pluginruntime.NewManager(pluginDir, pluginruntime.DefaultLoader{
		Root: pluginDir,
		CapabilityFunc: func(id string) (pluginruntime.Capability, bool, error) {
			return a.plugins.Capability(id)
		},
		DirFunc: a.plugins.Dir,
	}, sec)
	a.cap.SetEnv([]string{"OPENCRAFT_VERSION=" + app.ServiceVersion})
	a.cap.SetInferenceHandler(pluginruntime.InferenceHandler{
		Upsert: func(pluginID string, profile pluginruntime.InferenceProfile) error {
			if err := a.upsertInferenceProfile(pluginID, profile); err != nil {
				return err
			}
			// Notify before rebuild so the settings page reflects the
			// config change even if the rebuild fails.
			if a.bridge != nil {
				a.bridge.Emit("inference_changed", map[string]any{})
			}
			return a.requestRebuild()
		},
		Remove: func(_, id string) error {
			if err := a.removeInferenceProfile(id); err != nil {
				return err
			}
			if a.bridge != nil {
				a.bridge.Emit("inference_changed", map[string]any{})
			}
			return a.requestRebuild()
		},
	})
	return a, nil
}

// Startup is called by Wails once the window context exists.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.bridge.SetContext(ctx)
	if a.cap != nil {
		a.cap.SetOpenURL(func(url string) {
			if a.ctx != nil {
				wailsruntime.BrowserOpenURL(a.ctx, url)
			}
		})
	}
	// Route stream/interact events to their owning conversation so a
	// frontend reload can recover mid-run routing; delegated subagent
	// runs resolve to "" and stay out of the chat.
	a.bridge.SetRunConvResolver(func(runID string) string {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.runConvs[runID]
	})
	a.bridge.SetRollout(a.onStreamRollout)
	a.openUserDB()
	if err := a.rebuild(); err != nil {
		a.bridge.Emit("fatal", map[string]any{"error": err.Error()})
	}
	// Reconcile inference keys with the credential store: migrate
	// literals into the store and clear dangling references. This runs
	// after the runtime is assembled and off the startup path, so a
	// slow or unavailable credential store can never delay the session
	// list or block the first turn; config changes take effect on the
	// next rebuild/start.
	go a.reconcileInferenceKeys()

	// The tray icon is a background-resident app's primary entry point;
	// start it once the window context exists so its actions can reach
	// the Wails runtime.
	a.startTray()
}

// Shutdown tears down the runtime when the window closes.
func (a *App) Shutdown(ctx context.Context) {
	a.stopTray()
	a.closeRollouts()
	// Stop scheduling before tearing down the runtime: in-flight runs
	// are killed by closeRuntime and their records are reconciled on
	// the next launch.
	if m := a.automationManagerRef(); m != nil {
		m.Stop()
	}
	a.closeBackgroundHosts()
	a.closeRuntime()
	a.mu.Lock()
	udb := a.userDB
	usageStore := a.usage
	a.userDB = nil
	a.usage = nil
	a.automationStore = nil
	a.automations = nil
	a.mu.Unlock()
	if udb != nil {
		_ = udb.Close()
	} else if usageStore != nil {
		_ = usageStore.Close()
	}
	if a.otelShutdown != nil {
		flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = a.otelShutdown(flushCtx)
	}
}

// openUserDB opens ~/.opencraft/user.db once at startup and wires the
// usage and automation stores to the shared connection. Failures are
// best-effort: the window still opens, and automation bindings report
// "unavailable" until the store is fixed.
func (a *App) openUserDB() {
	dataDir, err := config.UserDataDir()
	if err != nil {
		return
	}
	udb, err := userdb.Open(filepath.Join(dataDir, "user.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: open user db: %v\n", err)
		return
	}
	usageStore, err := usage.New(udb.SQLDB())
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: usage schema: %v\n", err)
	}
	autoStore, err := automations.New(udb.SQLDB())
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: automation schema: %v\n", err)
	}
	if usageStore == nil && autoStore == nil {
		_ = udb.Close()
		return
	}
	a.mu.Lock()
	a.userDB = udb
	a.usage = usageStore
	a.automationStore = autoStore
	if autoStore != nil {
		mgr, mgrErr := automations.NewManager(autoStore, automations.ManagerOptions{
			Run:    a.runAutomation,
			Window: 2 * time.Minute,
			Limit:  4,
			OnChange: func() {
				if a.bridge != nil {
					a.bridge.Emit("automation_changed", map[string]any{})
				}
			},
			OnRun: func(r automations.Run) {
				if a.bridge != nil {
					a.bridge.Emit("automation_run", toAutomationRunDTO(r))
				}
			},
		})
		if mgrErr != nil {
			fmt.Fprintf(os.Stderr, "opencraft: automation manager: %v\n", mgrErr)
		} else {
			a.automations = mgr
			mgr.Start()
		}
	}
	a.mu.Unlock()
}

// rebuild loads the user configuration layer and assembles a fresh UI
// runtime. Inference wiring is not required to start: an unconfigured
// install builds with the embedded router shell and the UI guides the
// user to the settings page.
func (a *App) rebuild() error {
	a.closeRuntime()

	a.mu.Lock()
	wd := a.workDir
	a.mu.Unlock()
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(wd) == "" {
		// No workspace selected yet: the runtime, undo store, and
		// session store are all workspace-bound, so there is nothing
		// to assemble. Emit ready with an empty work_dir and let the
		// UI show the workspace picker instead of the chat.
		if a.bridge != nil {
			a.bridge.Emit("ready", a.status(true))
		}
		return nil
	}
	// The user-level usage database is workspace-independent and opened
	// once per app run. Open it outside a.mu: sqlite open executes a
	// PRAGMA that can block on a stale database lock, and holding the
	// app-wide mutex across that would freeze every binding.
	a.mu.Lock()
	usageStore := a.usage
	a.mu.Unlock()
	if usageStore == nil {
		if dataDir, err := config.UserDataDir(); err == nil {
			if store, err := usage.Open(filepath.Join(dataDir, "user.db")); err == nil {
				a.mu.Lock()
				if a.usage == nil {
					a.usage = store
					store = nil
				}
				a.mu.Unlock()
				if store != nil {
					_ = store.Close()
				}
			}
		}
	}
	assembled, err := a.assembleRuntime(ctx, wd, a.bridge, a.onUsage)
	if err != nil {
		return err
	}
	// Wire the runtime's artifact observer to the frontend bridge so
	// successful workspace writes stream as "artifact" UI events (the
	// observing workspace already filters out engine-internal writes).
	if assembled.artifacts != nil {
		assembled.artifacts.SetSink(a.onArtifactWrite)
	}

	a.mu.Lock()
	a.ctrl = assembled.ctrl
	a.broker = assembled.broker
	a.sessions = assembled.store
	a.agents = assembled.lifecycle
	a.mu.Unlock()

	a.bridge.Emit("ready", a.status(true))
	return nil
}

// refreshAgentPlugins applies plugin-driven agent capability changes.
// Rebuilds are deferred while any turn (user or automation) runs.
func (a *App) refreshAgentPlugins() error {
	return a.requestRebuild()
}

// maybeApplyPendingRebuild runs after a turn ends and applies the
// deferred workspace switch or runtime rebuild once no turn runs.
func (a *App) maybeApplyPendingRebuild() {
	a.mu.Lock()
	if len(a.turns) > 0 {
		a.mu.Unlock()
		return
	}
	pending := a.pendingRebuild
	wd := a.pendingWorkDir
	a.pendingRebuild = false
	a.pendingWorkDir = ""
	a.mu.Unlock()
	if wd != "" {
		if err := a.applyOpenWorkspace(wd); err != nil {
			fmt.Fprintf(os.Stderr,
				"opencraft: deferred workspace switch failed: %v\n", err)
		}
		return
	}
	if !pending {
		return
	}
	if err := a.rebuild(); err != nil {
		fmt.Fprintf(os.Stderr,
			"opencraft: deferred rebuild failed: %v\n", err)
	}
}

// requestRebuild applies a runtime rebuild immediately when no turn
// is active and defers it otherwise, so user turns and unattended
// automation runs are never killed by configuration changes.
func (a *App) requestRebuild() error {
	a.mu.Lock()
	active := len(a.turns) > 0
	if active {
		a.pendingRebuild = true
	}
	a.mu.Unlock()
	// Background hosts never hot-reload: idle ones are closed now and
	// busy ones are marked stale (reaped after their last run), so the
	// next dispatch assembles with the latest configuration.
	a.invalidateBackgroundHosts()
	if active {
		return nil
	}
	return a.rebuild()
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
	a.pendingRebuild = false
	a.pendingWorkDir = ""
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
	lifecycle := a.agents
	a.mu.Unlock()
	agents := 0
	if lifecycle != nil {
		// List scans the agent registry directories on disk; do it
		// outside the app-wide lock.
		agents = len(lifecycle.List())
	}
	defaultReasoning := false
	if cfg, err := config.LoadInference(ud); err == nil {
		defaultReasoning = cfg.ModelReasoning("")
	}
	st := ConfigStatus{
		Needed:           !configured,
		DefaultModel:     config.DefaultModel(ud),
		DefaultReasoning: defaultReasoning,
		WorkDir:          wd,
		UserDir:          ud,
		Version:          app.ServiceVersion,
		Agents:           agents,
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
