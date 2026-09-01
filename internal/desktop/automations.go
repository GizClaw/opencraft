package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/opencraft/internal/automations"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

// AutomationScheduleDTO is the wire form of one task schedule.
type AutomationScheduleDTO struct {
	Type          string   `json:"type"`
	IntervalHours int      `json:"interval_hours,omitempty"`
	IntervalWeeks int      `json:"interval_weeks,omitempty"`
	Days          []string `json:"days,omitempty"`
	Time          string   `json:"time,omitempty"`
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

// Automations returns every task with its next run time.
func (a *App) Automations() ([]AutomationTaskDTO, error) {
	store := a.automationStoreRef()
	if store == nil {
		return nil, errors.New("automations are unavailable")
	}
	tasks, err := store.ListTasks(a.appContext())
	if err != nil {
		return nil, err
	}
	out := make([]AutomationTaskDTO, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toAutomationTaskDTO(t))
	}
	return out, nil
}

// SaveAutomation creates or updates one task. System fields
// (created_at, last_run_at, next_run_at, ...) are always owned by the
// backend.
func (a *App) SaveAutomation(dto AutomationTaskDTO) (AutomationTaskDTO, error) {
	store := a.automationStoreRef()
	if store == nil {
		return AutomationTaskDTO{}, errors.New("automations are unavailable")
	}
	workspace := strings.TrimSpace(dto.Workspace)
	if workspace == "" {
		return AutomationTaskDTO{}, errors.New("workspace is required")
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return AutomationTaskDTO{}, fmt.Errorf("workspace %s does not exist", workspace)
	}
	task := automations.Task{
		ID:             strings.TrimSpace(dto.ID),
		Name:           dto.Name,
		Prompt:         dto.Prompt,
		Schedule:       fromScheduleDTO(dto.Schedule),
		Workspace:      workspace,
		Mode:           dto.Mode,
		Model:          strings.TrimSpace(dto.Model),
		Think:          strings.TrimSpace(dto.Think),
		ConversationID: strings.TrimSpace(dto.ConversationID),
		Notify:         dto.Notify,
		Enabled:        dto.Enabled,
	}
	if task.ConversationID != "" {
		ok, err := a.sessionExistsInWorkspace(workspace, task.ConversationID)
		if err != nil {
			return AutomationTaskDTO{}, fmt.Errorf("check session: %w", err)
		}
		if !ok {
			return AutomationTaskDTO{}, fmt.Errorf(
				"session %s does not exist in workspace", task.ConversationID)
		}
	}
	saved, err := store.SaveTask(a.appContext(), task)
	if err != nil {
		return AutomationTaskDTO{}, err
	}
	a.emitAutomationChanged()
	return toAutomationTaskDTO(saved), nil
}

// DeleteAutomation removes one task and its run history.
func (a *App) DeleteAutomation(id string) error {
	store := a.automationStoreRef()
	if store == nil {
		return errors.New("automations are unavailable")
	}
	if m := a.automationManagerRef(); m != nil {
		m.Discard(id)
	}
	if err := store.DeleteTask(a.appContext(), id); err != nil {
		return err
	}
	a.emitAutomationChanged()
	return nil
}

// RunAutomationNow queues one task immediately. It does not move the
// scheduled anchor.
func (a *App) RunAutomationNow(id string) error {
	m := a.automationManagerRef()
	if m == nil {
		return errors.New("automations are unavailable")
	}
	task, err := m.Task(id)
	if err != nil {
		return err
	}
	if !task.Enabled {
		return errors.New("task is disabled")
	}
	return m.RunNow(id)
}

// AutomationRuns returns one task's run history, newest first.
func (a *App) AutomationRuns(taskID string) ([]AutomationRunDTO, error) {
	store := a.automationStoreRef()
	if store == nil {
		return nil, errors.New("automations are unavailable")
	}
	runs, err := store.ListRuns(a.appContext(), taskID)
	if err != nil {
		return nil, err
	}
	out := make([]AutomationRunDTO, 0, len(runs))
	for _, r := range runs {
		out = append(out, toAutomationRunDTO(r))
	}
	return out, nil
}

// AutomationSessions lists the sessions of one workspace for the task
// form's existing-session picker.
func (a *App) AutomationSessions(workspace string) ([]SessionMeta, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return []SessionMeta{}, nil
	}
	root := filepath.Join(workspace, ".opencraft", "sessions")
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return []SessionMeta{}, nil
	}
	store, err := ocsessions.New(root, 40)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	metas, err := store.List()
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(metas))
	for _, m := range metas {
		out = append(out, SessionMeta{
			ID:          m.ID,
			Title:       m.Title,
			CreatedAt:   m.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   m.UpdatedAt.Format(time.RFC3339),
			Messages:    m.Messages,
			TotalTokens: m.Usage.TotalTokens,
		})
	}
	return out, nil
}

// sessionExistsInWorkspace reports whether the session directory
// exists under the workspace's session root.
func (a *App) sessionExistsInWorkspace(workspace, id string) (bool, error) {
	root := filepath.Join(workspace, ".opencraft", "sessions")
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return false, nil
	}
	store, err := ocsessions.New(root, 40)
	if err != nil {
		return false, err
	}
	defer func() { _ = store.Close() }()
	return store.Exists(id), nil
}

// runAutomation executes one task on the background runtime for its
// workspace. The task never reuses the UI runtime, so it runs even
// when task.Workspace is not the currently open workspace.
func (a *App) runAutomation(
	_ context.Context, task automations.Task,
) (automations.RunResult, error) {
	// The scheduler passes a background context; tie the run to the
	// app lifecycle instead so shutdown cancels the wait.
	ctx := a.appContext()
	failed := func(err error) (automations.RunResult, error) {
		return automations.RunResult{Status: automations.RunFailed}, err
	}
	host, err := a.backgroundHostFor(task.Workspace)
	if err != nil {
		return failed(err)
	}
	mode := ocsessions.Mode(task.Mode)
	if mode == "" {
		mode = ocsessions.ModeWorkspace
	}
	done := make(chan TurnEnd, 1)
	if _, err := host.runTurn(
		ctx, task.Prompt, mode, task.Think, task.Model,
		task.ConversationID, done,
	); err != nil {
		return failed(err)
	}
	select {
	case end := <-done:
		res := automations.RunResult{
			ConversationID: end.ConversationID,
			RunID:          end.RunID,
		}
		switch {
		case end.Status == "completed":
			res.Status = automations.RunCompleted
		case end.Error != "":
			res.Status = automations.RunFailed
			res.Error = end.Error
		default:
			res.Status = automations.RunFailed
			res.Error = "turn " + end.Status
		}
		if a.inCurrentWorkspace(task.Workspace) {
			// The run targets the currently open workspace: surface it
			// as an ordinary main session (turn_end clears its busy
			// state, refreshes the session list, and carries the
			// task's notify policy).
			notify := !a.suppressAutomationNotify(task, end)
			end.Notify = &notify
			if a.bridge != nil {
				a.bridge.Emit("turn_end", end)
			}
		} else {
			a.emitAutomationNotify(task, end)
		}
		return res, nil
	case <-ctx.Done():
		return failed(ctx.Err())
	}
}

// emitAutomationNotify sends the background run's notification event
// for runs outside the currently open workspace, which never surface
// as main sessions.
func (a *App) emitAutomationNotify(task automations.Task, end TurnEnd) {
	if a.suppressAutomationNotify(task, end) || a.bridge == nil {
		return
	}
	a.bridge.Emit("automation_notify", map[string]any{
		"task_id": task.ID,
		"name":    task.Name,
		"status":  end.Status,
		"error":   end.Error,
		"output":  end.Output,
	})
}

// suppressAutomationNotify reports whether the task's notify policy
// suppresses a notification for this outcome.
func (a *App) suppressAutomationNotify(task automations.Task, end TurnEnd) bool {
	switch task.Notify {
	case automations.NotifyNever:
		return true
	case automations.NotifyFailed:
		return end.Status == "completed"
	}
	return false
}

// inCurrentWorkspace reports whether wd is the workspace currently open
// in the main UI.
func (a *App) inCurrentWorkspace(wd string) bool {
	a.mu.Lock()
	cur := a.workDir
	a.mu.Unlock()
	return cur != "" && filepath.Clean(cur) == filepath.Clean(wd)
}

func (a *App) automationStoreRef() *automations.Store {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.automationStore
}

func (a *App) automationManagerRef() *automations.Manager {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.automations
}

func (a *App) emitAutomationChanged() {
	if a.bridge != nil {
		a.bridge.Emit("automation_changed", map[string]any{})
	}
}

func toAutomationTaskDTO(t automations.Task) AutomationTaskDTO {
	return AutomationTaskDTO{
		ID:             t.ID,
		Name:           t.Name,
		Prompt:         t.Prompt,
		Schedule:       toScheduleDTO(t.Schedule),
		Workspace:      t.Workspace,
		Mode:           t.Mode,
		Model:          t.Model,
		Think:          t.Think,
		ConversationID: t.ConversationID,
		Notify:         t.Notify,
		Enabled:        t.Enabled,
		CreatedAt:      fmtTime(t.CreatedAt),
		UpdatedAt:      fmtTime(t.UpdatedAt),
		LastRunAt:      fmtTime(t.LastRunAt),
		LastStatus:     t.LastStatus,
		NextRunAt:      fmtTime(t.NextRunAt),
	}
}

func fromScheduleDTO(d AutomationScheduleDTO) automations.Schedule {
	return automations.Schedule{
		Type:          automations.ScheduleType(d.Type),
		IntervalHours: d.IntervalHours,
		IntervalWeeks: d.IntervalWeeks,
		Days:          d.Days,
		Time:          d.Time,
	}
}

func toScheduleDTO(s automations.Schedule) AutomationScheduleDTO {
	return AutomationScheduleDTO{
		Type:          string(s.Type),
		IntervalHours: s.IntervalHours,
		IntervalWeeks: s.IntervalWeeks,
		Days:          s.Days,
		Time:          s.Time,
	}
}

func toAutomationRunDTO(r automations.Run) AutomationRunDTO {
	return AutomationRunDTO{
		ID:             r.ID,
		TaskID:         r.TaskID,
		At:             fmtTime(r.At),
		Status:         string(r.Status),
		Error:          r.Error,
		ConversationID: r.ConversationID,
		RunID:          r.RunID,
		DurationMs:     r.DurationMs,
		Summary:        r.Summary,
	}
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
