package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
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

// automationStreamState tracks one background run's final output and
// forwards stream deltas to the UI only when the run targets the
// currently open workspace.
type automationStreamState struct {
	a *App

	mu     sync.Mutex
	output strings.Builder
}

func (s *automationStreamState) sink(
	ctx context.Context,
	env event.Envelope,
	delta agent.StreamDeltaPayload,
) error {
	if !agent.IsStreamDelta(env.Subject) {
		return nil
	}
	runID := streamRunID(env.Subject)
	if delta.Type == agent.StreamDeltaPart {
		if part, ok := delta.Part.(message.TextPart); ok {
			s.mu.Lock()
			next := s.output.String() + part.Text
			if len(next) > 8000 {
				next = next[len(next)-8000:]
			}
			s.output.Reset()
			s.output.WriteString(next)
			s.mu.Unlock()
		}
	}
	if s.a.bridge != nil && s.a.inCurrentWorkspace(s.a.snapshotWorkDir()) {
		s.a.bridge.Emit("stream", StreamEvent{
			RunID:          runID,
			ConversationID: s.a.runConversation(runID),
			Delta:          delta,
		})
	}
	return nil
}

func (s *automationStreamState) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.output.String())
}

// runConversation resolves the owning conversation for a stream event
// without holding the app lock while Host reads run state.
func (a *App) runConversation(runID string) string {
	return a.activeConversationFor(runID)
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
	root := a.workspaceSessionsRoot(workspace)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return []SessionMeta{}, nil
	}
	store, err := a.openSessionStore(context.Background(), root)
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
	root := a.workspaceSessionsRoot(workspace)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return false, nil
	}
	store, err := a.openSessionStore(context.Background(), root)
	if err != nil {
		return false, err
	}
	defer func() { _ = store.Close() }()
	return store.Exists(id), nil
}

// openSessionStore opens one workspace sessions.Store with the
// centralized workspace migration (schema + legacy JSON import)
// applied. It is used by the automation session picker, which reads
// workspaces that are not necessarily open in the main UI.
func (a *App) openSessionStore(
	ctx context.Context, root string,
) (*ocsessions.Store, error) {
	store, err := ocsessions.New(root, 40)
	if err != nil {
		return nil, err
	}
	if err := migrations.Workspace(ctx, store.Database(), root); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// runAutomation executes one task on the shared Host for its
// workspace. A task targeting the currently open workspace reuses the
// UI Host; other workspaces get a pooled Host from the same manager.
func (a *App) runAutomation(
	_ context.Context, task automations.Task,
) (automations.RunResult, error) {
	// The scheduler passes a background context; tie the run to the
	// app lifecycle instead so shutdown cancels the wait.
	ctx := a.appContext()
	failed := func(err error) (automations.RunResult, error) {
		return automations.RunResult{Status: automations.RunFailed}, err
	}
	h, release, err := a.hostForRun(task.Workspace)
	if err != nil {
		return failed(err)
	}
	defer release()
	mode := ocsessions.Mode(task.Mode)
	if mode == "" {
		mode = ocsessions.ModeWorkspace
	}
	stream := &automationStreamState{a: a}
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:   message.NewTextMessage(message.RoleUser, task.Prompt),
		ContextID: task.ConversationID,
		Mode:      mode,
		Think:     task.Think,
		Model:     task.Model,
		Sink:      agent.StreamSinkFunc(stream.sink),
		QueueSize: 256,
		OnUsage:   a.onUsage,
		Backend:   interact.Auto{},
	})
	if err != nil {
		return failed(err)
	}
	runID := run.RunID()
	contextID := run.ContextID()
	current := a.inCurrentWorkspace(task.Workspace)
	if current {
		a.mu.Lock()
		if a.convRuns[contextID] == nil {
			a.convRuns[contextID] = make(map[string]bool)
		}
		a.convRuns[contextID][runID] = true
		a.mu.Unlock()
		if a.bridge != nil {
			a.bridge.Emit("automation_run_started", map[string]any{
				"run_id":          runID,
				"conversation_id": contextID,
			})
		}
	}
	res, waitErr := run.Wait(ctx)
	a.mu.Lock()
	turnUsage := a.runUsage[runID]
	delete(a.runUsage, runID)
	usageStore := a.usage
	a.mu.Unlock()

	finishedAt := time.Now().UTC()
	end := TurnEnd{
		RunID:          runID,
		ConversationID: contextID,
		Status:         "unknown",
		Output:         stream.text(),
		FinishedAt:     finishedAt.Format(time.RFC3339),
	}
	if res != nil {
		end.Status = string(res.Status)
		if res.Err != nil {
			end.Error = res.Err.Error()
		}
	}
	if waitErr != nil && end.Error == "" {
		end.Error = waitErr.Error()
	}
	if usageStore != nil && turnUsage.Model != "" {
		_ = usageStore.Record(
			ctx,
			workspaceID(h.WorkDir()),
			contextID,
			turnUsage.Model,
			usage.Usage{
				InputTokens:     turnUsage.InputTokens,
				OutputTokens:    turnUsage.OutputTokens,
				CacheReadTokens: turnUsage.CacheReadTokens,
				ReasoningTokens: turnUsage.ReasoningTokens,
				LatencyMs:       turnUsage.LatencyMs,
			},
		)
	}
	if current {
		notify := !a.suppressAutomationNotify(task, end)
		end.Notify = &notify
		if a.bridge != nil {
			a.bridge.Emit("turn_end", end)
		}
		a.emitUndoState(contextID)
		a.maybeApplyPendingRebuild()
	} else {
		a.emitAutomationNotify(task, end)
	}
	res2 := automations.RunResult{
		ConversationID: end.ConversationID,
		RunID:          end.RunID,
	}
	switch {
	case end.Status == "completed":
		res2.Status = automations.RunCompleted
	case end.Error != "":
		res2.Status = automations.RunFailed
		res2.Error = end.Error
	default:
		res2.Status = automations.RunFailed
		res2.Error = "turn " + end.Status
	}
	return res2, nil
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
