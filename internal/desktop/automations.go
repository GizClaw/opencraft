package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/automations"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

// AutomationScheduleDTO is the wire form of one task schedule.
type AutomationScheduleDTO struct {
	Type          string   `json:"type"`
	IntervalHours int      `json:"interval_hours,omitempty"`
	Days          []string `json:"days,omitempty"`
	Time          string   `json:"time,omitempty"`
	Cron          string   `json:"cron,omitempty"`
}

// AutomationTaskDTO is the wire form of one automation task.
type AutomationTaskDTO struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Prompt     string                `json:"prompt"`
	Schedule   AutomationScheduleDTO `json:"schedule"`
	Workspace  string                `json:"workspace"`
	Mode       string                `json:"mode"`
	Model      string                `json:"model"`
	Think      string                `json:"think"`
	Notify     string                `json:"notify"`
	Enabled    bool                  `json:"enabled"`
	CreatedAt  string                `json:"created_at"`
	UpdatedAt  string                `json:"updated_at"`
	LastRunAt  string                `json:"last_run_at"`
	LastStatus string                `json:"last_status"`
	NextRunAt  string                `json:"next_run_at"`
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
		ID:        strings.TrimSpace(dto.ID),
		Name:      dto.Name,
		Prompt:    dto.Prompt,
		Schedule:  fromScheduleDTO(dto.Schedule),
		Workspace: workspace,
		Mode:      dto.Mode,
		Model:     strings.TrimSpace(dto.Model),
		Think:     strings.TrimSpace(dto.Think),
		Notify:    dto.Notify,
		Enabled:   dto.Enabled,
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
	wd := a.snapshotWorkDir()
	if filepath.Clean(wd) != filepath.Clean(task.Workspace) {
		return fmt.Errorf("workspace mismatch: open %s first", task.Workspace)
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

// runAutomation executes one task through the existing turn pipeline
// in its own conversation and waits for the turn to finish.
func (a *App) runAutomation(
	ctx context.Context, task automations.Task,
) (automations.RunResult, error) {
	failed := func(err error) (automations.RunResult, error) {
		return automations.RunResult{Status: automations.RunFailed}, err
	}
	a.mu.Lock()
	ctrl := a.ctrl
	broker := a.broker
	store := a.sessions
	wd := a.workDir
	a.mu.Unlock()
	if ctrl == nil || broker == nil || store == nil {
		return failed(errors.New("runtime 未就绪"))
	}
	if filepath.Clean(wd) != filepath.Clean(task.Workspace) {
		return automations.RunResult{
			Status: automations.RunSkipped,
			Error:  "工作区未打开：" + task.Workspace,
		}, nil
	}
	contextID := ocsessions.NewID()
	a.mu.Lock()
	if a.automationConvs == nil {
		a.automationConvs = make(map[string]string)
	}
	a.automationConvs[contextID] = task.ID
	a.mu.Unlock()
	mode := ocsessions.Mode(task.Mode)
	if mode == "" {
		mode = ocsessions.ModeWorkspace
	}
	if err := store.SetMode(contextID, mode); err != nil {
		return failed(err)
	}
	if task.Think != "" {
		if err := store.SetThink(ctx, contextID, ocsessions.ThinkLevel(task.Think)); err != nil {
			return failed(err)
		}
	}
	if task.Model != "" {
		if err := store.SetModel(contextID, task.Model); err != nil {
			return failed(err)
		}
	}
	msg := message.Message{
		Role: message.RoleUser,
		Content: message.Content{
			Parts: []message.Part{message.TextPart{Text: task.Prompt}},
		},
	}
	done := make(chan TurnEnd, 1)
	if _, err := a.startTurn(
		msg, contextID, mode, task.Think, task.Model, wd, done,
	); err != nil {
		return failed(err)
	}
	select {
	case end := <-done:
		res := automations.RunResult{
			ConversationID: contextID,
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
		return res, nil
	case <-ctx.Done():
		return failed(ctx.Err())
	}
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

// applyAutomationNotify resolves one finished automation turn's task
// and, when the task suppresses notifications for this outcome, marks
// the turn_end payload so the frontend skips the system banner.
func (a *App) applyAutomationNotify(contextID string, end *TurnEnd) {
	a.mu.Lock()
	taskID := a.automationConvs[contextID]
	delete(a.automationConvs, contextID)
	store := a.automationStore
	a.mu.Unlock()
	if taskID == "" || store == nil {
		return
	}
	task, err := store.GetTask(a.appContext(), taskID)
	if err != nil {
		return
	}
	suppress := false
	switch task.Notify {
	case automations.NotifyNever:
		suppress = true
	case automations.NotifyFailed:
		suppress = end.Status == "completed"
	}
	if suppress {
		end.Notify = &suppress
	}
}

func toAutomationTaskDTO(t automations.Task) AutomationTaskDTO {
	return AutomationTaskDTO{
		ID:         t.ID,
		Name:       t.Name,
		Prompt:     t.Prompt,
		Schedule:   toScheduleDTO(t.Schedule),
		Workspace:  t.Workspace,
		Mode:       t.Mode,
		Model:      t.Model,
		Think:      t.Think,
		Notify:     t.Notify,
		Enabled:    t.Enabled,
		CreatedAt:  fmtTime(t.CreatedAt),
		UpdatedAt:  fmtTime(t.UpdatedAt),
		LastRunAt:  fmtTime(t.LastRunAt),
		LastStatus: t.LastStatus,
		NextRunAt:  fmtTime(t.NextRunAt),
	}
}

func fromScheduleDTO(d AutomationScheduleDTO) automations.Schedule {
	return automations.Schedule{
		Type:          automations.ScheduleType(d.Type),
		IntervalHours: d.IntervalHours,
		Days:          d.Days,
		Time:          d.Time,
		Cron:          d.Cron,
	}
}

func toScheduleDTO(s automations.Schedule) AutomationScheduleDTO {
	return AutomationScheduleDTO{
		Type:          string(s.Type),
		IntervalHours: s.IntervalHours,
		Days:          s.Days,
		Time:          s.Time,
		Cron:          s.Cron,
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
