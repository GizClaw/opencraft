package core

import (
	"context"
	"errors"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
)

func TestAutomationHostLifecycle(t *testing.T) {
	ctx := context.Background()
	r := NewRuntime(t.TempDir(), t.TempDir())
	if err := r.OpenUserDB(ctx); err != nil {
		t.Fatalf("open user db: %v", err)
	}
	host := NewAutomationHost(r)

	task := automations.Task{
		Name:      "test-brief",
		Prompt:    "run the test",
		Schedule:  automations.Schedule{Type: automations.ScheduleDaily, Time: "09:00"},
		Workspace: "/tmp/workspace",
	}
	preview, err := host.AutomationsPreview(ctx, "create", task)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Mode != automations.ModeWorkspace ||
		preview.Notify != automations.NotifyAlways {
		t.Fatalf("preview defaults = mode %q notify %q",
			preview.Mode, preview.Notify)
	}

	saved, err := host.AutomationsApply(ctx, "create", preview)
	if err != nil {
		t.Fatalf("apply create: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("create did not assign an id")
	}
	tasks, err := host.AutomationsList(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != saved.ID {
		t.Fatalf("list = %+v, want one task %q", tasks, saved.ID)
	}

	got, err := host.AutomationsGet(ctx, saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "test-brief" {
		t.Fatalf("got name %q", got.Name)
	}

	if _, err := host.AutomationsApply(ctx, "delete", got); err != nil {
		t.Fatalf("apply delete: %v", err)
	}
	if _, err := host.AutomationsGet(ctx, saved.ID); !errors.Is(err,
		automations.ErrNotFound) {
		t.Fatalf("get after delete err = %v, want ErrNotFound", err)
	}
}

func TestAutomationHostRequiresOpenUserDB(t *testing.T) {
	r := NewRuntime(t.TempDir(), t.TempDir())
	host := NewAutomationHost(r)
	if _, err := host.AutomationsList(context.Background()); err == nil {
		t.Fatal("list before OpenUserDB must fail")
	}
	if _, err := host.AutomationsPreview(
		context.Background(), "update",
		automations.Task{Name: "x", Prompt: "p",
			Schedule: automations.Schedule{
				Type: automations.ScheduleDaily, Time: "08:00",
			},
			Workspace: "/tmp/w",
		},
	); err != nil {
		t.Fatalf("preview should validate before store access: %v", err)
	}
}
