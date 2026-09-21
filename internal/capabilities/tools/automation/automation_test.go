package automation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/foundation/interact"
)

type fakeHost struct {
	tasks   []automations.Task
	preview func(action string, task automations.Task) (automations.Task, error)
	applied []string
	apply   func(action string, task automations.Task) (automations.Task, error)
}

func (f *fakeHost) AutomationsList(
	context.Context,
) ([]automations.Task, error) {
	return f.tasks, nil
}

func (f *fakeHost) AutomationsGet(
	_ context.Context, id string,
) (automations.Task, error) {
	for _, t := range f.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return automations.Task{}, automations.ErrNotFound
}

func (f *fakeHost) AutomationsPreview(
	_ context.Context, action string, task automations.Task,
) (automations.Task, error) {
	if f.preview != nil {
		return f.preview(action, task)
	}
	task.ID = "t-preview"
	return task, nil
}

func (f *fakeHost) AutomationsApply(
	_ context.Context, action string, task automations.Task,
) (automations.Task, error) {
	f.applied = append(f.applied, action)
	if f.apply != nil {
		return f.apply(action, task)
	}
	task.ID = "t-saved"
	return task, nil
}

func confirmCtx(t *testing.T, choice string, cancelled bool) context.Context {
	t.Helper()
	meta := map[string]string{}
	if cancelled {
		meta[interact.MetaStatus] = string(interact.ReplyCancelled)
	} else {
		meta[interact.MetaChoice] = choice
	}
	return agent.ContextWithHost(context.Background(), agent.HostFuncs{
		AskUserFn: func(
			context.Context, agent.UserPrompt,
		) (agent.UserReply, error) {
			return agent.UserReply{Metadata: meta}, nil
		},
	})
}

func TestAutomationToolList(t *testing.T) {
	host := &fakeHost{tasks: []automations.Task{{
		ID: "t-1", Name: "brief",
		Schedule: automations.Schedule{
			Type: automations.ScheduleDaily, Time: "09:00",
		},
		Workspace: "/tmp/w", Mode: automations.ModeWorkspace,
		Enabled: true, Notify: automations.NotifyAlways,
	}}}
	out, err := New(host).Execute(
		context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []taskView
	if err := json.Unmarshal([]byte(out.Text()), &tasks); err != nil {
		t.Fatalf("list output is not JSON: %v\n%s", err, out)
	}
	if len(tasks) != 1 || tasks[0].Name != "brief" {
		t.Fatalf("tasks = %+v", tasks)
	}
}

func TestAutomationToolCreateConfirmed(t *testing.T) {
	host := &fakeHost{}
	ctx := confirmCtx(t, "yes", false)
	out, err := New(host).Execute(ctx,
		`{"action":"create","task":{"name":"brief","prompt":"run","workspace":"/tmp/w"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text(), `"id":"t-saved"`) {
		t.Fatalf("create output = %s", out)
	}
	if len(host.applied) != 1 || host.applied[0] != "create" {
		t.Fatalf("applied = %v", host.applied)
	}
}

func TestAutomationToolCreateCancelled(t *testing.T) {
	host := &fakeHost{}
	out, err := New(host).Execute(
		confirmCtx(t, "", true),
		`{"action":"create","task":{"name":"brief"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text(), `"cancelled":true`) {
		t.Fatalf("cancel output = %s", out)
	}
	if len(host.applied) != 0 {
		t.Fatalf("apply must not run after cancel, got %v", host.applied)
	}
}

func TestAutomationToolConfirmSendsOptions(t *testing.T) {
	var got agent.UserPrompt
	ctx := agent.ContextWithHost(context.Background(), agent.HostFuncs{
		AskUserFn: func(
			_ context.Context, p agent.UserPrompt,
		) (agent.UserReply, error) {
			got = p
			return agent.UserReply{
				Metadata: map[string]string{interact.MetaChoice: "yes"},
			}, nil
		},
	})
	host := &fakeHost{}
	if _, err := New(host).Execute(ctx,
		`{"action":"create","task":{"name":"brief"}}`); err != nil {
		t.Fatal(err)
	}
	opts := got.Metadata[interact.MetaOptions]
	if !strings.Contains(opts, `"value":"yes"`) ||
		!strings.Contains(opts, `"value":"no"`) {
		t.Fatalf("confirm options missing Yes/No: %q", opts)
	}
}

func TestAutomationToolDeleteConfirmed(t *testing.T) {
	host := &fakeHost{
		preview: func(action string, task automations.Task) (automations.Task, error) {
			task.Name = "brief"
			return task, nil
		},
	}
	out, err := New(host).Execute(
		confirmCtx(t, "yes", false),
		`{"action":"delete","task":{"id":"t-1"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text(), `"deleted":true`) {
		t.Fatalf("delete output = %s", out)
	}
	if len(host.applied) != 1 || host.applied[0] != "delete" {
		t.Fatalf("applied = %v", host.applied)
	}
}

func TestAutomationToolRequiresHost(t *testing.T) {
	if _, err := New(&fakeHost{}).Execute(
		context.Background(),
		`{"action":"create","task":{"name":"brief"}}`,
	); err == nil {
		t.Fatal("create without an AskUser host must fail")
	}
}

// schemaNode is the part of a JSON Schema this test reads back.
type schemaNode struct {
	Type        string                `json:"type"`
	Description string                `json:"description"`
	Enum        []string              `json:"enum"`
	Properties  map[string]schemaNode `json:"properties"`
}

// TestAutomationToolDefinitionCarriesNestedFields pins the schema the
// model reads. The nested task/schedule objects are built as raw schema
// maps because a ToolPropertyDef keeps its schema unexported: placed
// below the top level one marshals as an empty object, and the model is
// handed a property that exists by name only — no type, no description,
// no enum. The tool is the only place a field like `timeout` is
// announced at all, so its wording is what tells the model that an
// empty value means the 15-minute default rather than "unbounded".
func TestAutomationToolDefinitionCarriesNestedFields(t *testing.T) {
	def := New(&fakeHost{}).Definition()
	var schema schemaNode
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("tool schema is not JSON: %v", err)
	}
	task, ok := schema.Properties["task"]
	if !ok {
		t.Fatalf("schema has no task property: %s", def.InputSchema)
	}
	if len(task.Properties) == 0 {
		t.Fatalf("task property is empty: %s", def.InputSchema)
	}
	for name, prop := range task.Properties {
		if prop.Type == "" {
			t.Errorf("task.%s carries no type: %+v", name, prop)
		}
	}
	timeout, ok := task.Properties["timeout"]
	if !ok {
		t.Fatalf("task schema has no timeout property: %s", def.InputSchema)
	}
	if timeout.Type != "string" ||
		!strings.Contains(timeout.Description, "15-minute default") {
		t.Fatalf("task.timeout = %+v, want a string naming the default",
			timeout)
	}
	// The schedule enum is the part a model gets wrong most often, and
	// it only reaches the model through this nested object.
	schedule := task.Properties["schedule"]
	if schedule.Type != "object" || len(schedule.Properties) == 0 {
		t.Fatalf("task.schedule = %+v, want an object with fields", schedule)
	}
	kind := schedule.Properties["type"]
	if kind.Type != "string" || len(kind.Enum) != 4 {
		t.Fatalf("task.schedule.type = %+v, want the enum of rule kinds", kind)
	}
}

// TestPreviewSummaryRendersTimeout pins the confirm prompt: the bound is
// shown as minutes, and an unset limit says so instead of printing a
// number the task does not carry.
func TestPreviewSummaryRendersTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout string
		want    string
	}{
		{name: "unset shows the default", timeout: "", want: "timeout: 15m (default)"},
		{name: "explicit bound", timeout: "2h", want: "timeout: 120m"},
		{name: "minutes stay minutes", timeout: "45m", want: "timeout: 45m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := previewSummary(automations.Task{
				Name:    "brief",
				Prompt:  "summarize",
				Timeout: tc.timeout,
			})
			if !strings.Contains(got, tc.want) {
				t.Fatalf("preview = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}
