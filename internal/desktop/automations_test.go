package desktop

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/GizClaw/opencraft/internal/automations"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

func newAutomationTestApp(t *testing.T) (*App, *automations.Store) {
	t.Helper()
	wd := t.TempDir()
	store, err := automations.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	a := &App{
		mu:              sync.Mutex{},
		workDir:         wd,
		automationStore: store,
	}
	return a, store
}

func automationDTO(name, workspace string) AutomationTaskDTO {
	return AutomationTaskDTO{
		Name:      name,
		Prompt:    "生成简报，不要提问",
		Schedule:  AutomationScheduleDTO{Type: "daily", Time: "09:00"},
		Workspace: workspace,
		Mode:      automations.ModeWorkspace,
		Enabled:   true,
	}
}

func TestSaveAutomationRoundTrip(t *testing.T) {
	a, _ := newAutomationTestApp(t)
	dto := automationDTO("brief", a.workDir)
	saved, err := a.SaveAutomation(dto)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("id not generated")
	}
	if saved.NextRunAt == "" {
		t.Fatal("next run not computed")
	}
	tasks, err := a.Automations()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != saved.ID {
		t.Fatalf("tasks = %+v", tasks)
	}

	// Update keeps the id and renames the task.
	saved.Name = "renamed"
	updated, err := a.SaveAutomation(saved)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != saved.ID || updated.Name != "renamed" {
		t.Fatalf("update = %+v", updated)
	}

	if err := a.DeleteAutomation(saved.ID); err != nil {
		t.Fatal(err)
	}
	tasks, err = a.Automations()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks after delete = %+v", tasks)
	}
}

func TestSaveAutomationRejectsBadInput(t *testing.T) {
	a, _ := newAutomationTestApp(t)
	dto := automationDTO("bad", filepath.Join(a.workDir, "missing"))
	if _, err := a.SaveAutomation(dto); err == nil {
		t.Fatal("missing workspace must be rejected")
	}
	dto = automationDTO("bad", a.workDir)
	dto.Schedule = AutomationScheduleDTO{Type: "weekly", Time: "10:00"} // no days
	if _, err := a.SaveAutomation(dto); err == nil {
		t.Fatal("invalid schedule must be rejected")
	}
}

func TestSaveAutomationExistingSession(t *testing.T) {
	a, _ := newAutomationTestApp(t)
	dto := automationDTO("brief", a.workDir)
	dto.ConversationID = "s-missing"
	if _, err := a.SaveAutomation(dto); err == nil {
		t.Fatal("session outside the workspace must be rejected")
	}

	root := filepath.Join(a.workDir, ".opencraft", "sessions")
	store, err := ocsessions.New(root, 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	_ = store.Close()
	if err != nil {
		t.Fatal(err)
	}
	dto.ConversationID = id
	if _, err := a.SaveAutomation(dto); err != nil {
		t.Fatalf("SaveAutomation with existing session: %v", err)
	}
}

func TestAutomationRunsBinding(t *testing.T) {
	a, store := newAutomationTestApp(t)
	saved, err := store.SaveTask(context.Background(), automations.Task{
		Name:      "brief",
		Prompt:    "任务",
		Schedule:  automations.Schedule{Type: automations.ScheduleDaily, Time: "09:00"},
		Workspace: a.workDir,
		Mode:      automations.ModeWorkspace,
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRun(context.Background(), automations.Run{
		TaskID: saved.ID,
		Status: automations.RunCompleted,
	}); err != nil {
		t.Fatal(err)
	}
	runs, err := a.AutomationRuns(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != string(automations.RunCompleted) {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestRunAutomationNowGuard(t *testing.T) {
	a, store := newAutomationTestApp(t)
	if err := a.RunAutomationNow("t-1"); err == nil {
		t.Fatal("RunAutomationNow without manager must fail")
	}
	m, err := automations.NewManager(store, automations.ManagerOptions{
		Run: func(context.Context, automations.Task) (automations.RunResult, error) {
			return automations.RunResult{Status: automations.RunCompleted}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.automations = m
	a.mu.Unlock()

	task, err := store.SaveTask(context.Background(), automations.Task{
		Name:      "brief",
		Prompt:    "任务",
		Schedule:  automations.Schedule{Type: automations.ScheduleDaily, Time: "09:00"},
		Workspace: filepath.Join(a.workDir, "other"),
		Mode:      automations.ModeWorkspace,
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RunAutomationNow(task.ID); err != nil {
		t.Fatalf("RunAutomationNow on a non-open workspace must enqueue: %v", err)
	}
	task2 := task
	task2.Workspace = a.workDir
	if _, err := store.SaveTask(context.Background(), task2); err != nil {
		t.Fatal(err)
	}
	task2.Enabled = false
	if _, err := store.SaveTask(context.Background(), task2); err != nil {
		t.Fatal(err)
	}
	if err := a.RunAutomationNow(task2.ID); err == nil {
		t.Fatal("disabled task must fail")
	}
	task2.Enabled = true
	if _, err := store.SaveTask(context.Background(), task2); err != nil {
		t.Fatal(err)
	}
	if err := a.RunAutomationNow(task2.ID); err != nil {
		t.Fatalf("RunAutomationNow on valid task: %v", err)
	}
}

func TestAutomationToolHostLifecycle(t *testing.T) {
	a, store := newAutomationTestApp(t)
	ctx := context.Background()
	host := &automationHostAdapter{app: a}

	preview, err := host.AutomationsPreview(ctx, "create", automations.Task{
		Name:      "agent-brief",
		Prompt:    "任务",
		Schedule:  automations.Schedule{Type: automations.ScheduleDaily, Time: "09:00"},
		Workspace: a.workDir,
		Mode:      automations.ModeWorkspace,
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := host.AutomationsApply(ctx, "create", preview)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("created task has no id")
	}

	preview, err = host.AutomationsPreview(ctx, "update", automations.Task{
		ID:        saved.ID,
		Name:      "renamed",
		Prompt:    "任务2",
		Schedule:  saved.Schedule,
		Workspace: a.workDir,
		Mode:      automations.ModeWorkspace,
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := host.AutomationsApply(ctx, "update", preview)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "renamed" {
		t.Fatalf("updated name = %q", updated.Name)
	}

	del, err := host.AutomationsPreview(ctx, "delete", automations.Task{ID: saved.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.AutomationsApply(ctx, "delete", del); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetTask(ctx, saved.ID); err != automations.ErrNotFound {
		t.Fatalf("GetTask after delete err = %v, want ErrNotFound", err)
	}
}

func TestAutomationToolHostAgentPolicy(t *testing.T) {
	a, _ := newAutomationTestApp(t)
	host := &automationHostAdapter{app: a}
	ctx := context.Background()
	base := automations.Task{
		Name:      "x",
		Prompt:    "p",
		Schedule:  automations.Schedule{Type: automations.ScheduleDaily, Time: "09:00"},
		Workspace: a.workDir,
		Mode:      automations.ModeWorkspace,
		Enabled:   true,
	}

	yolo := base
	yolo.Mode = automations.ModeYOLO
	if _, err := host.AutomationsPreview(ctx, "create", yolo); err == nil {
		t.Fatal("agent-created yolo task must be rejected")
	}

	other := base
	other.Workspace = filepath.Join(t.TempDir(), "other")
	if _, err := host.AutomationsPreview(ctx, "create", other); err == nil {
		t.Fatal("agent-created task in an unknown workspace must be rejected")
	}

	if _, err := host.AutomationsPreview(ctx, "create", base); err != nil {
		t.Fatalf("agent-created task in the open workspace must pass: %v", err)
	}
}
