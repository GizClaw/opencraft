// Package desktop hosts the opencraft desktop application: a Wails
// shell whose Go bindings drive the assembled flowcraft runtime and
// whose event bridge pushes runtime streams into the frontend. The
// package replaces the TUI/CLI entrypoints: the binary now starts the
// window, configuration lives in the GUI, and the execd child
// mode is handled by the root main package before Wails starts.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/capabilities/secrets"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/telemetry"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
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
	// TrayIcon is the system tray / menu bar icon (PNG). It is shown
	// as-is on macOS/Linux; Windows uses TrayIconWindows when set.
	// Nil skips the icon (tests).
	TrayIcon []byte
	// TrayIconWindows is the Windows system tray icon (.ico).
	// Windows systray loads .ico files, so PNG bytes would not render.
	// Nil falls back to TrayIcon (tests and other platforms).
	TrayIconWindows []byte
}

// App is the Wails-bound application root. Exported methods on App
// become JS bindings; everything else is internal state.
type App struct {
	ctx context.Context

	mu      sync.Mutex
	workDir string
	userDir string
	// plugins / kv / auth are the plugin subsystem entry points.
	plugins *plugins.Store
	kv      *plugins.KVStore
	// cap hosts subprocess capability plugins (e.g. the SSO auth
	// protocol). Lazily started on first PluginInvoke.
	cap *pluginruntime.Manager

	bridge       *Bridge
	otelShutdown func(context.Context) error

	hostMgr     *host.Manager
	currentHost *host.Host
	// sessions mirrors the current Host's store for store-only tests
	// and legacy bindings; runtime code should use sessionStore().
	sessions        *ocsessions.Store
	usage           *usage.Store
	userDB          *db.DB
	automationStore *automations.Store
	automations     *automations.Manager
	secrets         *secrets.Manager
	// pendingRebuild defers any runtime rebuild (settings, MCP,
	// plugins, workspace switch) until no turn is running, so
	// unattended automation runs survive configuration changes.
	pendingRebuild bool
	// pendingWorkDir carries a workspace switch requested while a run
	// was active; it is applied once the last turn ends.
	pendingWorkDir string
	// pendingNoWorkspace carries a close-workspace request made while
	// a run was active; it is applied once the last turn ends.
	pendingNoWorkspace bool

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
	// runConvs is a test-only fallback for activeRunCount when no Host
	// is assembled; production routing reads Host.ActiveRuns.
	runConvs map[string]string
	// convRuns retains every run id minted per conversation for the
	// app lifetime, so delegation cards (which persist the caller run
	// id) can be attributed back to the conversation that spawned them
	// even after the calling turn ended.
	convRuns map[string]map[string]bool
	// runUsage accumulates inference usage per active run; it feeds the
	// user-level usage DB at turn end (Host already persists it into
	// the workspace session store).
	runUsage map[string]ocsessions.Usage
	// dataDir is the root passed to host.Manager; it is also the
	// workspace state root.
	dataDir string
	// sessionImportMu serializes session.import across the UI runtime
	// and background hosts. Store.Import writes history before memory
	// is seeded, so duplicate imports for one source must never race
	// the pending/complete state.
	sessionImportMu sync.Mutex

	// closeToTray persists the close behaviour ("hide to tray" vs
	// "quit"). It is read by the Wails close hook on the UI thread.
	closeToTray bool
	// quitting is set when the user has asked for a real quit (tray
	// Quit, macOS Cmd+Q/Dock Quit, or "quit" close mode). OnBeforeClose
	// then asks for confirmation instead of hiding to the tray again.
	quitting bool
	// quitConfirmed is set once the exit warning has been accepted, so
	// a second OnBeforeClose round (e.g. RequestClose -> runtime.Quit)
	// does not show the dialog again.
	quitConfirmed bool
	// language is the desktop UI language ("zh" or "en") for native
	// tray/menu and dialog copy. The frontend keeps it in sync.
	language string
	// trayIcon is the system tray icon bytes (nil in tests).
	trayIcon []byte
	// trayIconWindows is the Windows system tray icon bytes (nil in
	// tests and on non-Windows builds).
	trayIconWindows []byte
	// trayItems are the live tray menu rows, so SetLanguage can retitle
	// them after the frontend reports its detected language.
	trayItems *trayItems
	// trayEnd is the systray external-loop teardown function (nil in
	// tests); set by startTray, consumed by stopTray.
	trayEnd func()
}

// New creates the application shell. Runtime assembly is deferred to
// Startup so the Wails context is available for event emission.
func New(opts Options) (*App, error) {
	dataDir := opts.DataDir
	if dataDir == "" {
		var err error
		dataDir, err = config.UserDataDir()
		if err != nil {
			return nil, fmt.Errorf("desktop: user data dir: %w", err)
		}
	}
	historyDir, err := config.WorkspacesRoot(dataDir)
	if err != nil {
		historyDir = ""
	}
	workDir := startupWorkDir(opts.WorkDir, historyDir)
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
	language := prefs.Language
	if language == "" {
		language = defaultDesktopLanguage()
	}
	if _, err := config.EnsureUserConfig(); err != nil {
		return nil, fmt.Errorf("desktop: seed config: %w", err)
	}
	hostMgr := host.NewManagerAt(dataDir, userDir)
	sec := secrets.NewManager(filepath.Join(dataDir, "keyring"))
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
		workDir:         workDir,
		userDir:         userDir,
		plugins:         plugins.NewStore(pluginDir),
		kv:              plugins.NewKVStore(pluginDir),
		bridge:          NewBridge(),
		conversationID:  ocsessions.NewID(),
		mode:            ocsessions.ModeWorkspace,
		think:           string(ocsessions.ThinkMedium),
		model:           "",
		runConvs:        make(map[string]string),
		convRuns:        make(map[string]map[string]bool),
		runUsage:        make(map[string]ocsessions.Usage),
		hostMgr:         hostMgr,
		dataDir:         dataDir,
		secrets:         sec,
		otelShutdown:    shutdown,
		closeToTray:     prefs.CloseToTray,
		language:        language,
		trayIcon:        opts.TrayIcon,
		trayIconWindows: opts.TrayIconWindows,
	}
	a.plugins.SetHostVersion(telemetry.ServiceVersion)
	a.cap = pluginruntime.NewManager(pluginDir, pluginruntime.DefaultLoader{
		Root: pluginDir,
		CapabilityFunc: func(id string) (pluginruntime.Capability, bool, error) {
			return a.plugins.Capability(id)
		},
		DirFunc: a.plugins.Dir,
	}, sec)
	a.cap.SetEnv([]string{"OPENCRAFT_VERSION=" + telemetry.ServiceVersion})
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
	a.cap.SetSessionImportHandler(pluginruntime.SessionImportHandler{
		Import: a.handleSessionImport,
	})
	a.cap.SetWorkspaceHandler(pluginruntime.WorkspaceHandler{
		Current: func() (string, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.workDir, nil
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
		return a.activeConversationFor(runID)
	})
	a.configureHostManager()
	a.openUserDB()
	if err := a.rebuild(); err != nil {
		a.bridge.Emit("fatal", map[string]any{"error": err.Error()})
	}

	// The tray icon is a background-resident app's primary entry point;
	// start it once the window context exists so its actions can reach
	// the Wails runtime.
	a.startTray()
}

// Shutdown tears down the runtime when the window closes.
func (a *App) Shutdown(ctx context.Context) {
	a.stopTray()
	// Stop scheduling before tearing down the runtime: in-flight runs
	// are killed by closeRuntime and their records are reconciled on
	// the next launch.
	if m := a.automationManagerRef(); m != nil {
		m.Stop()
	}
	if a.hostMgr != nil {
		a.hostMgr.CancelAll()
		a.hostMgr.CloseAll()
	}
	a.closeRuntime()
	a.mu.Lock()
	udb := a.userDB
	a.userDB = nil
	a.usage = nil
	a.automationStore = nil
	a.automations = nil
	a.mu.Unlock()
	if udb != nil {
		_ = udb.Close()
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
	udb, err := db.Open(filepath.Join(dataDir, "user.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: open user db: %v\n", err)
		return
	}
	if err := migrations.User(context.Background(), udb); err != nil {
		_ = udb.Close()
		fmt.Fprintf(os.Stderr, "opencraft: user db migrations: %v\n", err)
		return
	}
	usageStore, err := usage.Attach(udb)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: usage schema: %v\n", err)
	}
	autoStore, err := automations.Attach(udb)
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
// runtime. Assembly is only attempted when a workspace is open and the
// merged deployment has inference targets; without a workspace or
// before inference is configured the app emits ready and the UI guides
// the user (the embedded router shell cannot validate without pools).
func (a *App) rebuild() error {
	a.closeRuntime()

	a.mu.Lock()
	wd := a.workDir
	a.mu.Unlock()
	ctx := a.appContext()
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
			udb, err := db.Open(filepath.Join(dataDir, "user.db"))
			if err == nil {
				if migrErr := migrations.User(ctx, udb); migrErr != nil {
					_ = udb.Close()
				} else if store, attachErr := usage.Attach(udb); attachErr == nil {
					a.mu.Lock()
					if a.usage == nil {
						a.usage = store
						a.userDB = udb
						udb = nil
					}
					a.mu.Unlock()
					if udb != nil {
						_ = udb.Close()
					}
				} else {
					_ = udb.Close()
				}
			}
		}
	}
	configured, err := a.inferenceConfigured()
	if err != nil {
		return err
	}
	if !configured {
		if a.bridge != nil {
			a.bridge.Emit("ready", a.status(false))
		}
		return nil
	}
	if a.hostMgr == nil {
		return errors.New("desktop: host manager is not configured")
	}
	h, err := a.hostMgr.Acquire(ctx, wd, a.bridge, nil)
	if err != nil {
		return err
	}
	a.configureHost(h)

	a.mu.Lock()
	a.currentHost = h
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
	if a.activeRunCount() > 0 {
		return
	}
	a.mu.Lock()
	pending := a.pendingRebuild
	wd := a.pendingWorkDir
	closeWorkspace := a.pendingNoWorkspace
	a.pendingRebuild = false
	a.pendingWorkDir = ""
	a.pendingNoWorkspace = false
	a.mu.Unlock()
	if closeWorkspace {
		if err := a.applyCloseWorkspace(); err != nil {
			fmt.Fprintf(os.Stderr,
				"opencraft: deferred workspace close failed: %v\n", err)
		}
		return
	}
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
	active := a.activeRunCount() > 0
	if active {
		a.mu.Lock()
		a.pendingRebuild = true
		a.mu.Unlock()
	}
	// Drop every pooled runtime: idle hosts close immediately, hosts
	// with active runs finish on the old configuration and close after
	// their last run ends.
	if a.hostMgr != nil {
		a.hostMgr.InvalidateAll()
	}
	if active {
		return nil
	}
	return a.rebuild()
}

func (a *App) closeRuntime() {
	a.mu.Lock()
	h := a.currentHost
	a.currentHost = nil
	a.runConvs = make(map[string]string)
	a.convRuns = make(map[string]map[string]bool)
	a.runUsage = make(map[string]ocsessions.Usage)
	a.pendingRebuild = false
	a.pendingWorkDir = ""
	a.pendingNoWorkspace = false
	a.mu.Unlock()

	if h != nil {
		_ = h.Close()
	}
}

func (a *App) status(configured bool) ConfigStatus {
	a.mu.Lock()
	wd := a.workDir
	ud := a.userDir
	a.mu.Unlock()
	lifecycle := a.agentLifecycle()
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
		Version:          telemetry.ServiceVersion,
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
	runID := ""
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		runID = info.RunID
	}
	if runID == "" {
		runID = a.bridge.LastStreamRun()
	}
	emit := runID != "" && a.isCurrentRun(runID)
	a.mu.Lock()
	if runID == "" {
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
	if emit && a.bridge != nil {
		a.bridge.Usage(u)
	}
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
	return telemetry.InitOtel(context.Background(), telemetry.TelemetryOptions{
		OTLPEndpoint: otelEndpoint,
		OTLPInsecure: otelInsecure,
		LogFile:      logPath,
	})
}
