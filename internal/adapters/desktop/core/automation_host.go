package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	automationtool "github.com/GizClaw/opencraft/internal/capabilities/tools/automation"
)

// automationHost adapts the desktop user-database store to the agent tool's
// Host contract. The store is attached by OpenUserDB, so every call resolves
// it through the runtime instead of caching a nil handle at startup.
type automationHost struct {
	runtime *Runtime
}

// NewAutomationHost builds the host used by engine assembly on the desktop.
func NewAutomationHost(r *Runtime) automationtool.Host {
	return &automationHost{runtime: r}
}

func (h *automationHost) store() (*automations.Store, error) {
	s := h.runtime.Automations()
	if s == nil {
		return nil, errors.New(
			"automation host: user database is not open")
	}
	return s, nil
}

// AutomationsList implements automationtool.Host.
func (h *automationHost) AutomationsList(
	ctx context.Context,
) ([]automations.Task, error) {
	s, err := h.store()
	if err != nil {
		return nil, err
	}
	return s.ListTasks(ctx)
}

// AutomationsGet implements automationtool.Host.
func (h *automationHost) AutomationsGet(
	ctx context.Context, id string,
) (automations.Task, error) {
	s, err := h.store()
	if err != nil {
		return automations.Task{}, err
	}
	return s.GetTask(ctx, id)
}

// AutomationsPreview implements automationtool.Host. It validates and
// normalizes create/update input without persisting; delete resolves the
// stored task so the confirmation prompt shows the real name.
func (h *automationHost) AutomationsPreview(
	ctx context.Context, action string, task automations.Task,
) (automations.Task, error) {
	switch strings.TrimSpace(action) {
	case "create", "update":
		task.Name = strings.TrimSpace(task.Name)
		task.Prompt = strings.TrimSpace(task.Prompt)
		task.Workspace = strings.TrimSpace(task.Workspace)
		if task.Mode == "" {
			task.Mode = automations.ModeWorkspace
		}
		if task.Notify == "" {
			task.Notify = automations.NotifyAlways
		}
		if err := task.Validate(); err != nil {
			return automations.Task{}, err
		}
		return task, nil
	case "delete":
		if strings.TrimSpace(task.ID) == "" {
			return automations.Task{}, errors.New(
				"automation delete requires a task id")
		}
		s, err := h.store()
		if err != nil {
			return automations.Task{}, err
		}
		return s.GetTask(ctx, task.ID)
	default:
		return automations.Task{}, fmt.Errorf(
			"unknown automation action %q", action)
	}
}

// AutomationsApply implements automationtool.Host.
func (h *automationHost) AutomationsApply(
	ctx context.Context, action string, task automations.Task,
) (automations.Task, error) {
	s, err := h.store()
	if err != nil {
		return automations.Task{}, err
	}
	switch strings.TrimSpace(action) {
	case "create", "update":
		return s.SaveTask(ctx, task)
	case "delete":
		if strings.TrimSpace(task.ID) == "" {
			return automations.Task{}, errors.New(
				"automation delete requires a task id")
		}
		return automations.Task{}, s.DeleteTask(ctx, task.ID)
	default:
		return automations.Task{}, fmt.Errorf(
			"unknown automation action %q", action)
	}
}
