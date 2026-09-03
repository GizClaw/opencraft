package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	automationtool "github.com/GizClaw/opencraft/internal/capabilities/tools/automation"
)

// automationHostAdapter implements automationtool.Host without
// exporting the methods on App: Wails binds every exported App method,
// and the Host signatures use automations.Task (which carries
// time.Time fields the Wails model generator cannot type).
type automationHostAdapter struct {
	app *App
}

var _ automationtool.Host = (*automationHostAdapter)(nil)

// AutomationsList implements automationtool.Host.
func (h *automationHostAdapter) AutomationsList(
	ctx context.Context,
) ([]automations.Task, error) {
	a := h.app
	store := a.automationStoreRef()
	if store == nil {
		return nil, errors.New("automations are unavailable")
	}
	return store.ListTasks(ctx)
}

// AutomationsGet implements automationtool.Host.
func (h *automationHostAdapter) AutomationsGet(
	ctx context.Context, id string,
) (automations.Task, error) {
	a := h.app
	store := a.automationStoreRef()
	if store == nil {
		return automations.Task{}, errors.New("automations are unavailable")
	}
	return store.GetTask(ctx, id)
}

// AutomationsPreview implements automationtool.Host: it validates and
// normalizes the proposed change without persisting anything.
func (h *automationHostAdapter) AutomationsPreview(
	ctx context.Context, action string, task automations.Task,
) (automations.Task, error) {
	a := h.app
	store := a.automationStoreRef()
	if store == nil {
		return automations.Task{}, errors.New("automations are unavailable")
	}
	switch action {
	case "create":
		task = normalizeAutomationTask(task)
		if err := a.validateAgentAutomationTask(ctx, task); err != nil {
			return automations.Task{}, err
		}
		if err := a.validateAutomationPreview(ctx, task); err != nil {
			return automations.Task{}, err
		}
		return task, nil
	case "update":
		if strings.TrimSpace(task.ID) == "" {
			return automations.Task{}, errors.New(
				"automation: update requires task.id")
		}
		existing, err := store.GetTask(ctx, task.ID)
		if err != nil {
			return automations.Task{}, err
		}
		task = normalizeAutomationTask(task)
		if task.Schedule.Origin == "" {
			task.Schedule.Origin = existing.Schedule.Origin
		}
		if err := a.validateAgentAutomationTask(ctx, task); err != nil {
			return automations.Task{}, err
		}
		if err := a.validateAutomationPreview(ctx, task); err != nil {
			return automations.Task{}, err
		}
		return task, nil
	case "delete":
		if strings.TrimSpace(task.ID) == "" {
			return automations.Task{}, errors.New(
				"automation: delete requires task.id")
		}
		return store.GetTask(ctx, task.ID)
	default:
		return automations.Task{}, fmt.Errorf(
			"automation: unknown action %q", action)
	}
}

// AutomationsApply implements automationtool.Host: it persists the
// confirmed change.
func (h *automationHostAdapter) AutomationsApply(
	ctx context.Context, action string, task automations.Task,
) (automations.Task, error) {
	a := h.app
	store := a.automationStoreRef()
	if store == nil {
		return automations.Task{}, errors.New("automations are unavailable")
	}
	switch action {
	case "create", "update":
		saved, err := store.SaveTask(ctx, task)
		if err != nil {
			return automations.Task{}, err
		}
		a.emitAutomationChanged()
		return saved, nil
	case "delete":
		if m := a.automationManagerRef(); m != nil {
			m.Discard(task.ID)
		}
		if err := store.DeleteTask(ctx, task.ID); err != nil {
			return automations.Task{}, err
		}
		a.emitAutomationChanged()
		return automations.Task{ID: task.ID}, nil
	default:
		return automations.Task{}, fmt.Errorf(
			"automation: unknown action %q", action)
	}
}

// normalizeAutomationTask applies the same defaults and trimming the
// UI save path uses.
func normalizeAutomationTask(task automations.Task) automations.Task {
	task.Name = strings.TrimSpace(task.Name)
	task.Prompt = strings.TrimSpace(task.Prompt)
	task.Workspace = strings.TrimSpace(task.Workspace)
	task.Model = strings.TrimSpace(task.Model)
	if task.Mode == "" {
		task.Mode = automations.ModeWorkspace
	}
	if task.Notify == "" {
		task.Notify = automations.NotifyAlways
	}
	return task
}

// validateAgentAutomationTask applies the extra safety policy for
// agent-created tasks: the workspace must be one the user has already
// opened (never an arbitrary path chosen by the model), and the
// unattended yolo mode is rejected.
func (a *App) validateAgentAutomationTask(
	ctx context.Context, task automations.Task,
) error {
	if task.Mode == automations.ModeYOLO {
		return errors.New(
			"automation: yolo mode is not allowed for agent-created tasks; " +
				"use workspace or read-only")
	}
	if !a.agentWorkspaceAllowed(ctx, task.Workspace) {
		return fmt.Errorf(
			"automation: workspace %s is not a known workspace; "+
				"use one of the user's opened workspaces", task.Workspace)
	}
	return nil
}

// agentWorkspaceAllowed reports whether wd is the currently open
// workspace or appears in the user's workspace history.
func (a *App) agentWorkspaceAllowed(ctx context.Context, wd string) bool {
	wd = filepath.Clean(wd)
	a.mu.Lock()
	cur := a.workDir
	a.mu.Unlock()
	if cur != "" && filepath.Clean(cur) == wd {
		return true
	}
	metas, err := a.Workspaces()
	if err != nil {
		return false
	}
	for _, m := range metas {
		if filepath.Clean(m.Path) == wd {
			return true
		}
	}
	return false
}

// validateAutomationPreview checks workspace existence, session
// ownership, and the task/schedule shape without touching the store.
func (a *App) validateAutomationPreview(
	ctx context.Context, task automations.Task,
) error {
	if task.Workspace == "" {
		return errors.New("workspace is required")
	}
	info, err := os.Stat(task.Workspace)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("workspace %s does not exist", task.Workspace)
	}
	if task.ConversationID != "" {
		ok, err := a.sessionExistsInWorkspace(
			task.Workspace, task.ConversationID)
		if err != nil {
			return fmt.Errorf("check session: %w", err)
		}
		if !ok {
			return fmt.Errorf(
				"session %s does not exist in workspace",
				task.ConversationID)
		}
	}
	return task.Validate()
}
