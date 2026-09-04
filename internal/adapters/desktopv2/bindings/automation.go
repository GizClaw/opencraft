package bindings

import (
	"strings"
	"time"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/automations"
)

// AutomationScheduleDTO is the wire form of one schedule.
type AutomationScheduleDTO struct {
	Type          string   `json:"type"`
	IntervalHours int      `json:"interval_hours,omitempty"`
	IntervalWeeks int      `json:"interval_weeks,omitempty"`
	Days          []string `json:"days,omitempty"`
	Time          string   `json:"time,omitempty"`
	Origin        string   `json:"origin,omitempty"`
}

// AutomationTaskDTO is the wire form of one automation task.
type AutomationTaskDTO struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Prompt         string                `json:"prompt"`
	Schedule       AutomationScheduleDTO `json:"schedule"`
	Workspace      string                `json:"workspace"`
	Mode           string                `json:"mode"`
	Model          string                `json:"model"`
	Think          string                `json:"think"`
	ConversationID string                `json:"conversation_id,omitempty"`
	Notify         string                `json:"notify"`
	Enabled        bool                  `json:"enabled"`
	CreatedAt      string                `json:"created_at"`
	UpdatedAt      string                `json:"updated_at"`
	LastRunAt      string                `json:"last_run_at"`
	LastStatus     string                `json:"last_status"`
	NextRunAt      string                `json:"next_run_at"`
}

// AutomationRunDTO is the wire form of one run record.
type AutomationRunDTO struct {
	ID             string `json:"id"`
	TaskID         string `json:"task_id"`
	At             string `json:"at"`
	Status         string `json:"status"`
	Error          string `json:"error"`
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
	DurationMs     int64  `json:"duration_ms"`
	Summary        string `json:"summary"`
}

// Automation exposes scheduled task CRUD over the user DB.
type Automation struct {
	core *core.Core
}

// NewAutomationBinding wires the automation binding.
func NewAutomationBinding(c *core.Core) *Automation {
	return &Automation{core: c}
}

// List returns every automation task.
func (b *Automation) List() ([]AutomationTaskDTO, error) {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Automations()
	if store == nil {
		return nil, errNotReady("automation")
	}
	tasks, err := store.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AutomationTaskDTO, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, ToAutomationTaskDTO(task))
	}
	return out, nil
}

// Save creates or updates one task.
func (b *Automation) Save(dto AutomationTaskDTO) (AutomationTaskDTO, error) {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Automations()
	if store == nil {
		return AutomationTaskDTO{}, errNotReady("automation")
	}
	task := FromAutomationTaskDTO(dto)
	saved, err := store.SaveTask(ctx, task)
	if err != nil {
		return AutomationTaskDTO{}, err
	}
	return ToAutomationTaskDTO(saved), nil
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
) ([]AutomationRunDTO, error) {
	ctx := b.core.Shell.Context()
	store := b.core.Runtime.Automations()
	if store == nil {
		return nil, errNotReady("automation")
	}
	runs, err := store.ListRuns(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]AutomationRunDTO, 0, len(runs))
	for _, run := range runs {
		out = append(out, ToAutomationRunDTO(run))
	}
	return out, nil
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
	mgr := b.core.Runtime.Manager()
	if mgr == nil {
		return nil, errNotReady("runtime")
	}
	store, err := mgr.OpenSessions(ctx, workspace, layout, 40)
	if err != nil {
		return nil, err
	}
	defer func() {
		mgr.ReleaseSessions(store)
	}()
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

// ToAutomationTaskDTO converts one stored task into its UI wire form.
func ToAutomationTaskDTO(task automations.Task) AutomationTaskDTO {
	return AutomationTaskDTO{
		ID:             task.ID,
		Name:           task.Name,
		Prompt:         task.Prompt,
		Schedule:       ToAutomationScheduleDTO(task.Schedule),
		Workspace:      task.Workspace,
		Mode:           task.Mode,
		Model:          task.Model,
		Think:          task.Think,
		ConversationID: task.ConversationID,
		Notify:         task.Notify,
		Enabled:        task.Enabled,
		CreatedAt:      fmtAutomationTime(task.CreatedAt),
		UpdatedAt:      fmtAutomationTime(task.UpdatedAt),
		LastRunAt:      fmtAutomationTime(task.LastRunAt),
		LastStatus:     task.LastStatus,
		NextRunAt:      fmtAutomationTime(task.NextRunAt),
	}
}

// FromAutomationTaskDTO maps the UI form onto a stored task. System
// timestamps are owned by the backend and left zero.
func FromAutomationTaskDTO(dto AutomationTaskDTO) automations.Task {
	return automations.Task{
		ID:             strings.TrimSpace(dto.ID),
		Name:           strings.TrimSpace(dto.Name),
		Prompt:         dto.Prompt,
		Schedule:       FromAutomationScheduleDTO(dto.Schedule),
		Workspace:      strings.TrimSpace(dto.Workspace),
		Mode:           dto.Mode,
		Model:          strings.TrimSpace(dto.Model),
		Think:          strings.TrimSpace(dto.Think),
		ConversationID: strings.TrimSpace(dto.ConversationID),
		Notify:         dto.Notify,
		Enabled:        dto.Enabled,
	}
}

// ToAutomationScheduleDTO converts one stored schedule.
func ToAutomationScheduleDTO(s automations.Schedule) AutomationScheduleDTO {
	return AutomationScheduleDTO{
		Type:          string(s.Type),
		IntervalHours: s.IntervalHours,
		IntervalWeeks: s.IntervalWeeks,
		Days:          s.Days,
		Time:          s.Time,
		Origin:        s.Origin,
	}
}

// FromAutomationScheduleDTO maps the UI schedule form.
func FromAutomationScheduleDTO(d AutomationScheduleDTO) automations.Schedule {
	return automations.Schedule{
		Type:          automations.ScheduleType(d.Type),
		IntervalHours: d.IntervalHours,
		IntervalWeeks: d.IntervalWeeks,
		Days:          d.Days,
		Time:          d.Time,
		Origin:        d.Origin,
	}
}

// ToAutomationRunDTO converts one stored run into its UI wire form.
func ToAutomationRunDTO(run automations.Run) AutomationRunDTO {
	return AutomationRunDTO{
		ID:             run.ID,
		TaskID:         run.TaskID,
		At:             fmtAutomationTime(run.At),
		Status:         string(run.Status),
		Error:          run.Error,
		ConversationID: run.ConversationID,
		RunID:          run.RunID,
		DurationMs:     run.DurationMs,
		Summary:        run.Summary,
	}
}

func fmtAutomationTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
