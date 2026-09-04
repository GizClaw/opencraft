package bindings

import (
	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
)

// Automation exposes scheduled task CRUD over the user DB.
type Automation struct {
	core *core.Core
}

// NewAutomationBinding wires the automation binding.
func NewAutomationBinding(c *core.Core) *Automation {
	return &Automation{core: c}
}

// List returns every automation task.
func (b *Automation) List() ([]automations.Task, error) {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Automations()
	if store == nil {
		return nil, errNotReady("automation")
	}
	return store.ListTasks(ctx)
}

// Save creates or updates one task.
func (b *Automation) Save(task automations.Task) (automations.Task, error) {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Automations()
	if store == nil {
		return automations.Task{}, errNotReady("automation")
	}
	return store.SaveTask(ctx, task)
}

// Delete removes one task and its run history.
func (b *Automation) Delete(id string) error {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Automations()
	if store == nil {
		return errNotReady("automation")
	}
	return store.DeleteTask(ctx, id)
}

// Runs returns one task's run history.
func (b *Automation) Runs(
	taskID string,
) ([]automations.Run, error) {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Automations()
	if store == nil {
		return nil, errNotReady("automation")
	}
	return store.ListRuns(ctx, taskID)
}

// RunNow queues one task for immediate execution.
func (b *Automation) RunNow(id string) error {
	mgr := b.core.Runtime.AutomationManager()
	if mgr == nil {
		return errNotReady("automation manager")
	}
	return mgr.RunNow(id)
}

// AutomationSessions lists sessions under one workspace.
func (b *Automation) AutomationSessions(
	workspace string,
) ([]SessionMeta, error) {
	ctx := b.core.Shell.Context()
	layout, err := b.core.ResolveLayout(workspace)
	if err != nil {
		return nil, err
	}
	store, err := ocsessions.New(layout.SessionsDir, 40)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	if err := migrations.Workspace(ctx, store.Database(), layout.SessionsDir); err != nil {
		return nil, err
	}
	metas, err := store.List()
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(metas))
	for _, m := range metas {
		out = append(out, toSessionMeta(m))
	}
	return out, nil
}
