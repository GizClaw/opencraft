package desktop

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/GizClaw/opencraft/internal/automations"
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
	dto.Schedule = AutomationScheduleDTO{Type: "cron", Cron: "bad expr"}
	if _, err := a.SaveAutomation(dto); err == nil {
		t.Fatal("invalid cron must be rejected")
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
	if err := a.RunAutomationNow(task.ID); err == nil {
		t.Fatal("workspace mismatch must fail")
	}
	task.Workspace = a.workDir
	task.Enabled = false
	if _, err := store.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := a.RunAutomationNow(task.ID); err == nil {
		t.Fatal("disabled task must fail")
	}
	task.Enabled = true
	if _, err := store.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := a.RunAutomationNow(task.ID); err != nil {
		t.Fatalf("RunAutomationNow on valid task: %v", err)
	}
}
