package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/delegation/kanban"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/hooks"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/skills"
)

// ---- think level ----

// GetThink returns the current conversation's reasoning effort
// (low/medium/high).
func (a *App) GetThink() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.think, nil
}

// SetThink switches the conversation's reasoning effort and persists
// it through the session store.
func (a *App) SetThink(level string) error {
	lv := ocsessions.ThinkLevel(level)
	if !lv.Valid() {
		return fmt.Errorf("unknown think level %q", level)
	}
	a.mu.Lock()
	a.think = string(lv)
	contextID := a.conversationID
	store := a.sessions
	a.mu.Unlock()
	if store != nil {
		return store.SetThink(a.appContext(), contextID, lv)
	}
	return nil
}

// ---- per-conversation model ----

// GetModel returns the current conversation's model hint
// ("provider/name", or "" for the default routing policy).
func (a *App) GetModel() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.model, nil
}

// SetModel switches the conversation's model hint and persists it
// through the session store. An empty value resets the conversation to
// the default routing policy.
func (a *App) SetModel(model string) error {
	model = strings.TrimSpace(model)
	a.mu.Lock()
	a.model = model
	contextID := a.conversationID
	store := a.sessions
	a.mu.Unlock()
	if store != nil {
		return store.SetModel(a.appContext(), contextID, model)
	}
	return nil
}

// ---- sandbox permissions ----

func (a *App) manager() (*app.Manager, error) {
	a.mu.Lock()
	ctrl := a.ctrl
	a.mu.Unlock()
	if ctrl == nil || ctrl.Runtime() == nil {
		return nil, errors.New("runtime is not ready")
	}
	value, ok := ctrl.Runtime().Resource("execpolicy")
	if !ok {
		return nil, errors.New("execpolicy resource is not wired")
	}
	mgr, ok := value.(*app.Manager)
	if !ok {
		return nil, errors.New("execpolicy resource has an unexpected type")
	}
	return mgr, nil
}

// Permissions returns the current sandbox allowlist rules (static
// plus dynamically approved commands).
func (a *App) Permissions() ([]string, error) {
	mgr, err := a.manager()
	if err != nil {
		return nil, err
	}
	return mgr.Rules(), nil
}

// AllowPermission adds one rule to the sandbox allowlist and persists
// it to the project approvals file.
func (a *App) AllowPermission(rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return errors.New("rule is required")
	}
	mgr, err := a.manager()
	if err != nil {
		return err
	}
	return mgr.AlwaysAllow(rule)
}

// DenyPermission removes one rule from the sandbox allowlist.
func (a *App) DenyPermission(rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return errors.New("rule is required")
	}
	mgr, err := a.manager()
	if err != nil {
		return err
	}
	return mgr.Remove(rule)
}

// ---- skills ----

// Skills returns the discovered skill registry for the config page.
func (a *App) Skills() ([]SkillDTO, error) {
	a.mu.Lock()
	ctrl := a.ctrl
	a.mu.Unlock()
	if ctrl == nil || ctrl.Runtime() == nil {
		return []SkillDTO{}, nil
	}
	value, ok := ctrl.Runtime().Resource("skills")
	if !ok {
		return []SkillDTO{}, nil
	}
	svc, ok := value.(*skills.Service)
	if !ok {
		return []SkillDTO{}, nil
	}
	items := svc.List()
	out := make([]SkillDTO, 0, len(items))
	owners := a.pluginSkillRootOwners()
	for _, s := range items {
		owner, _ := pluginSkillOwnerForPath(owners, s.Path)
		out = append(out, SkillDTO{
			Name:        s.Name,
			Description: s.Description,
			Scope:       s.Scope,
			Path:        s.Path,
			PluginID:    owner.ID,
			PluginName:  owner.Name,
		})
	}
	return out, nil
}

// DeleteSkill removes one non-builtin skill directory (by its
// SKILL.md path as listed by Skills) and reloads the registry.
func (a *App) DeleteSkill(skillPath string) error {
	a.mu.Lock()
	ctrl := a.ctrl
	a.mu.Unlock()
	if ctrl == nil || ctrl.Runtime() == nil {
		return errors.New("runtime is not ready")
	}
	if owner, ok := pluginSkillOwnerForPath(
		a.pluginSkillRootOwners(), skillPath,
	); ok {
		return fmt.Errorf(
			"skills: plugin %q owns this skill and it cannot be deleted",
			owner.ID,
		)
	}
	value, ok := ctrl.Runtime().Resource("skills")
	if !ok {
		return errors.New("skills resource is not available")
	}
	svc, ok := value.(*skills.Service)
	if !ok {
		return errors.New("skills resource is not available")
	}
	return svc.Delete(skillPath)
}

// InstallSkill clones a skill (git URL or local path) into the given
// scope and reloads the registry so it is usable immediately. subpath
// optionally selects one skill directory inside the repo (e.g.
// "skills/flowcraft-config"); empty installs the whole repo.
func (a *App) InstallSkill(repo, scope, subpath string) (string, error) {
	a.mu.Lock()
	ctrl := a.ctrl
	a.mu.Unlock()
	if ctrl == nil || ctrl.Runtime() == nil {
		return "", errors.New("runtime is not ready")
	}
	value, ok := ctrl.Runtime().Resource("skills")
	if !ok {
		return "", errors.New("skills resource is not available")
	}
	svc, ok := value.(*skills.Service)
	if !ok {
		return "", errors.New("skills resource is not available")
	}
	return svc.Install(repo, scope, subpath)
}

// ---- kanban actions ----

func (a *App) board() (*kanban.Board, error) {
	a.mu.Lock()
	ctrl := a.ctrl
	a.mu.Unlock()
	if ctrl == nil || ctrl.Runtime() == nil {
		return nil, errors.New("runtime is not ready")
	}
	value, ok := ctrl.Runtime().Resource("delegate.backend")
	if !ok {
		return nil, errors.New("delegation backend is not wired")
	}
	board, ok := value.(*kanban.Board)
	if !ok {
		return nil, errors.New("delegation backend has an unexpected type")
	}
	return board, nil
}

// CancelCard cancels one delegation card.
func (a *App) CancelCard(id string) (bool, error) {
	board, err := a.board()
	if err != nil {
		return false, err
	}
	return board.Cancel(id, "cancelled from the desktop board"), nil
}

// ---- session deletion ----

// DeleteSession removes one conversation end to end: the runtime
// session manager's durable checkpoint state (history board, parked
// run, resumable request) is deleted by key, then the session store's
// directory and settings row are removed. The active conversation
// cannot be deleted.
func (a *App) DeleteSession(id string) error {
	a.mu.Lock()
	store := a.sessions
	ctrl := a.ctrl
	current := a.conversationID
	a.mu.Unlock()
	if store == nil {
		return errors.New("session store is not available")
	}
	if id == current {
		return errors.New("cannot delete the active conversation")
	}
	ctx := a.appContext()
	if ctrl != nil && ctrl.Runtime() != nil {
		key := coresession.Key{AgentID: "assistant", ContextID: id}
		drainCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := ctrl.Runtime().Sessions().DeleteSession(drainCtx, key); err != nil {
			return fmt.Errorf("delete runtime session: %w", err)
		}
	}
	if err := store.Remove(ctx, id); err != nil {
		return err
	}
	a.fireHooks(a.appContext(), hooks.EventSessionEnd, map[string]any{
		"event":           hooks.EventSessionEnd,
		"reason":          "delete",
		"conversation_id": id,
	})
	a.mu.Lock()
	rec := a.rollouts[id]
	delete(a.rollouts, id)
	a.mu.Unlock()
	if rec != nil {
		_ = rec.Close()
	}
	return nil
}

// ---- workspace ----

// OpenWorkspace switches the workspace root and rebuilds the runtime
// (config discovery, sandbox root, and the session store all follow).
// When a turn is running (user or automation), the switch is deferred
// until the last turn ends so unattended runs are never killed.
func (a *App) OpenWorkspace(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("workspace path is required")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	a.mu.Lock()
	active := len(a.turns) > 0
	a.mu.Unlock()
	if active {
		a.mu.Lock()
		a.pendingWorkDir = dir
		a.pendingRebuild = true
		a.mu.Unlock()
		return nil
	}
	return a.applyOpenWorkspace(dir)
}

func (a *App) applyOpenWorkspace(dir string) error {
	a.mu.Lock()
	a.workDir = dir
	previous := a.conversationID
	a.mu.Unlock()
	// closeRollouts takes a.mu itself; calling it while holding the
	// lock would self-deadlock (sync.Mutex is not reentrant).
	a.closeRollouts()
	a.fireHooks(a.appContext(), hooks.EventSessionEnd, map[string]any{
		"event":           hooks.EventSessionEnd,
		"reason":          "workspace_switch",
		"conversation_id": previous,
	})
	if err := a.requestRebuild(); err != nil {
		return err
	}
	// Remember the switch (best-effort) so the next launch restores
	// this workspace. Recording here rather than in rebuild keeps the
	// history to explicitly opened workspaces — a startup default can
	// never re-record itself.
	a.recordWorkspace(dir)
	return nil
}

// ChooseWorkspace opens the native folder picker and switches the
// workspace when the user picks one. An empty result means cancelled.
func (a *App) ChooseWorkspace() (string, error) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return "", errors.New("app context is not ready")
	}
	path, err := wailsruntime.OpenDirectoryDialog(
		ctx, wailsruntime.OpenDialogOptions{
			Title:            "选择工作区",
			DefaultDirectory: a.dialogStartDir(),
		})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	if err := a.OpenWorkspace(path); err != nil {
		return "", err
	}
	return path, nil
}

// PickFolder opens the native directory picker without switching the
// workspace (unlike ChooseWorkspace). Used by plugin install and other
// path-selection UIs.
func (a *App) PickFolder(title string) (string, error) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return "", errors.New("app context is not ready")
	}
	if title == "" {
		title = "选择文件夹"
	}
	return wailsruntime.OpenDirectoryDialog(
		ctx, wailsruntime.OpenDialogOptions{
			Title:            title,
			DefaultDirectory: a.dialogStartDir(),
		})
}

// PickFile opens the native file picker with an optional extension
// filter (e.g. "*.zip").
func (a *App) PickFile(title, pattern string) (string, error) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return "", errors.New("app context is not ready")
	}
	if title == "" {
		title = "选择文件"
	}
	var filters []wailsruntime.FileFilter
	if pattern != "" {
		filters = []wailsruntime.FileFilter{{
			DisplayName: pattern,
			Pattern:     pattern,
		}}
	}
	return wailsruntime.OpenFileDialog(
		ctx, wailsruntime.OpenDialogOptions{
			Title:            title,
			DefaultDirectory: a.dialogStartDir(),
			Filters:          filters,
		})
}

// ReadLog returns the tail of the application log file.
func (a *App) ReadLog(n int) (string, error) {
	if n <= 0 {
		n = 200
	}
	dataDir, err := config.UserDataDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "logs", "opencraft.log"))
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}
