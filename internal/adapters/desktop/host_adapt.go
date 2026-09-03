package desktop

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"

	"github.com/GizClaw/opencraft/internal/capabilities/agents"
	pluginagent "github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/undo"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/engine"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

var errNoHostManager = errors.New("desktop: host manager is not configured")

// configureHostManager injects adapter-owned engine options (plugin
// agent host and automation tool host) before the first runtime is
// assembled. Host itself stays desktop-free.
func (a *App) configureHostManager() {
	if a.hostMgr == nil {
		return
	}
	a.hostMgr.SetEngineOptionsFunc(func() []engine.Option {
		return []engine.Option{
			engine.WithAgentPlugins(
				pluginagent.NewHost(a.appContext(), a.plugins, a.cap),
			),
			engine.WithAutomationHost(&automationHostAdapter{app: a}),
		}
	})
	a.hostMgr.SetUsageObserver(func(ctx context.Context, usage inference.Usage) {
		if a.bridge != nil {
			a.bridge.Usage(usage)
		}
	})
}

// inferenceConfigured reports whether the merged deployment has at
// least one usable inference target. It mirrors the old desktop
// readiness check without assembling a runtime.
func (a *App) inferenceConfigured() (bool, error) {
	mgr, err := config.Open(config.Options{
		UserDir: a.userDir,
	})
	if err != nil {
		return false, err
	}
	view, err := mgr.Load(a.appContext())
	if err != nil {
		return false, err
	}
	return config.RouterConfigured(view.Document)
}

// activeRunCount returns the number of live runs on the currently open
// workspace Host.
func (a *App) activeRunCount() int {
	a.mu.Lock()
	h := a.currentHost
	if h == nil {
		n := len(a.runConvs) // test-only fallback
		a.mu.Unlock()
		return n
	}
	a.mu.Unlock()
	return len(h.ActiveRuns())
}

// activeConversationFor resolves the conversation of a run on the
// current Host. Delegated subagent runs are not owned by Host and
// return "".
func (a *App) activeConversationFor(runID string) string {
	a.mu.Lock()
	h := a.currentHost
	a.mu.Unlock()
	if h == nil {
		return ""
	}
	for _, r := range h.ActiveRuns() {
		if r.RunID == runID {
			return r.ConversationID
		}
	}
	return ""
}

// isCurrentRun reports whether runID is live on the current Host.
func (a *App) isCurrentRun(runID string) bool {
	return a.activeConversationFor(runID) != ""
}

// currentConversationID returns the UI-selected conversation id.
func (a *App) currentConversationID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conversationID
}

// controller returns the runtime controller of the current Host, or
// nil before a workspace is assembled.
func (a *App) controller() *engine.Controller {
	a.mu.Lock()
	h := a.currentHost
	a.mu.Unlock()
	if h == nil {
		return nil
	}
	return h.Controller()
}

// agentLifecycle returns the current Host's agent registry.
func (a *App) agentLifecycle() *agents.Lifecycle {
	a.mu.Lock()
	h := a.currentHost
	a.mu.Unlock()
	if h == nil {
		return nil
	}
	return h.Agents()
}

// sessionStore returns the current Host's session store, falling back
// to the cached mirror used by store-only tests.
func (a *App) sessionStore() *ocsessions.Store {
	a.mu.Lock()
	h := a.currentHost
	store := a.sessions
	a.mu.Unlock()
	if h != nil {
		if s := h.Sessions(); s != nil {
			return s
		}
	}
	return store
}

// configureHost wires adapter-only UI callbacks onto one Host.
func (a *App) configureHost(h *host.Host) {
	if h == nil {
		return
	}
	workDir := h.WorkDir()
	h.SetSessionUpdated(func(ctx context.Context, contextID string) {
		if a.bridge == nil || !a.inCurrentWorkspace(workDir) {
			return
		}
		a.bridge.Emit("session_updated", map[string]string{"id": contextID})
	})
	h.SetArtifactObserver(func(ctx context.Context, path string, data []byte) {
		if a.bridge == nil || !a.inCurrentWorkspace(workDir) {
			return
		}
		info, ok := agent.RunInfoFromContext(ctx)
		if !ok || info.ConversationID == "" {
			return
		}
		a.bridge.Emit("artifact", map[string]any{
			"conversation_id": info.ConversationID,
			"path":            path,
			"bytes":           len(data),
		})
	})
}

// hostForRun returns the shared Host for one workspace. When wd is the
// currently open workspace the current Host is reused without adding a
// manager reference; otherwise a pooled Host is acquired and release
// must be called when the run finishes.
func (a *App) hostForRun(wd string) (*host.Host, func(), error) {
	wd = filepath.Clean(wd)
	a.mu.Lock()
	h := a.currentHost
	if h != nil && a.workDir != "" &&
		filepath.Clean(a.workDir) == wd {
		a.mu.Unlock()
		return h, func() {}, nil
	}
	a.mu.Unlock()

	if a.hostMgr == nil {
		return nil, nil, errNoHostManager
	}
	h, err := a.hostMgr.Acquire(
		a.appContext(), wd, a.bridge, nil,
	)
	if err != nil {
		return nil, nil, err
	}
	a.configureHost(h)
	return h, func() { _ = h.Close() }, nil
}

// undoStoreFor builds the workspace-scoped undo store from the typed
// layout. Undo is a file-snapshot store without open handles, so the
// returned value is safe to create on demand and never needs Close.
func (a *App) undoStoreFor(wd string) *undo.Store {
	dataDir := a.dataDir
	if dataDir == "" {
		var err error
		dataDir, err = config.UserDataDir()
		if err != nil {
			return nil
		}
	}
	layout, err := config.ResolveWorkspace(dataDir, wd)
	if err != nil {
		return nil
	}
	_ = layout.Ensure()
	return undo.NewWithRoot(wd, layout.UndoDir)
}

// workspaceSessionsRoot returns the sessions directory for one
// workspace under the user data root. Manual test App values without
// dataDir fall back to the legacy project-local root.
func (a *App) workspaceSessionsRoot(wd string) string {
	if a.dataDir != "" {
		layout, err := config.ResolveWorkspace(a.dataDir, wd)
		if err == nil {
			return layout.SessionsDir
		}
	}
	return filepath.Join(wd, ".opencraft", "sessions")
}

// exportsDirFor returns the workspace export directory under the user
// data root, with the legacy project-local fallback for manual tests.
func (a *App) exportsDirFor(wd string) string {
	if a.dataDir != "" {
		layout, err := config.ResolveWorkspace(a.dataDir, wd)
		if err == nil {
			return layout.ExportsDir
		}
	}
	return filepath.Join(wd, ".opencraft", "exports")
}

// approvalsPathFor returns the workspace approvals file under the user
// data root, with the legacy project-local fallback for manual tests.
func (a *App) approvalsPathFor(wd string) string {
	if a.dataDir != "" {
		layout, err := config.ResolveWorkspace(a.dataDir, wd)
		if err == nil {
			return layout.ApprovalsFile
		}
	}
	return filepath.Join(wd, ".opencraft", "config", "approvals.yaml")
}

// workspaceCacheDirFor returns one workspace's tool cache directory
// under the user data root, with the legacy fallback for manual tests.
func (a *App) workspaceCacheDirFor(wd string) string {
	if a.dataDir != "" {
		layout, err := config.ResolveWorkspace(a.dataDir, wd)
		if err == nil {
			return layout.CacheDir
		}
	}
	return filepath.Join(wd, ".opencraft", "cache")
}
