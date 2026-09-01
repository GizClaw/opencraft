// Package automation exposes scheduled-task management to the agent:
// create / update / delete go through a preview + user-confirmation
// step before the change is persisted; list / get are read-only.
// Persistence is delegated to the desktop host, so the tool is a no-op
// (no tools exposed) in runtimes without a host.
package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/automations"
	"github.com/GizClaw/opencraft/internal/runtime"
)

// Name is the canonical tool name.
const Name = "automation"

// ResourceKind is the deploy resource kind of the automation host.
const ResourceKind = "opencraft.automations"

// ResourceImpl is the deploy impl id of the automation tool source.
const ResourceImpl = "opencraft/automation"

// Host is the desktop-side persistence surface the tool calls.
type Host interface {
	AutomationsList(ctx context.Context) ([]automations.Task, error)
	AutomationsGet(ctx context.Context, id string) (automations.Task, error)
	AutomationsPreview(
		ctx context.Context, action string, task automations.Task,
	) (automations.Task, error)
	AutomationsApply(
		ctx context.Context, action string, task automations.Task,
	) (automations.Task, error)
}

// Factory builds the opencraft.automations resource. A nil host yields
// an empty host so CLI/headless runtimes can still resolve the deploy
// graph; the tool source then exposes no tools.
type Factory struct {
	Host Host
}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: "local"}
}

// New implements resource.Factory.
func (f Factory) New(_ context.Context, _ resource.Input) (any, error) {
	if f.Host == nil {
		return emptyHost{}, nil
	}
	return f.Host, nil
}

// emptyHost reports that no desktop automation host is wired in.
type emptyHost struct{}

func (emptyHost) AutomationsList(context.Context) ([]automations.Task, error) {
	return nil, errdefs.NotAvailablef("automation: no host in this runtime")
}

func (emptyHost) AutomationsGet(
	context.Context, string,
) (automations.Task, error) {
	return automations.Task{}, errdefs.NotAvailablef(
		"automation: no host in this runtime")
}

func (emptyHost) AutomationsPreview(
	context.Context, string, automations.Task,
) (automations.Task, error) {
	return automations.Task{}, errdefs.NotAvailablef(
		"automation: no host in this runtime")
}

func (emptyHost) AutomationsApply(
	context.Context, string, automations.Task,
) (automations.Task, error) {
	return automations.Task{}, errdefs.NotAvailablef(
		"automation: no host in this runtime")
}

func (emptyHost) Empty() bool { return true }

// SourceFactory builds the tool source from the automation host
// resource.
type SourceFactory struct{}

var _ resource.Factory = SourceFactory{}

// Spec implements resource.Factory.
func (SourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: ResourceImpl,
		Deps: []resource.DepSpec{
			{Name: "automation.host", Type: ResourceKind, Required: true},
		},
	}
}

// New implements resource.Factory. An empty host contributes no tools.
func (SourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	dep, ok := in.Dep("automation.host")
	if !ok {
		return nil, errdefs.Validationf(
			"automation tools: automation.host dependency is required")
	}
	host, ok := dep.(Host)
	if !ok || host == nil {
		return nil, errdefs.Validationf(
			"automation tools: automation.host dep is %T, want Host", dep)
	}
	if empty, ok := host.(interface{ Empty() bool }); ok && empty.Empty() {
		return source{}, nil
	}
	return source{t: &Tool{host: host}}, nil
}

// source is one tool group containing the automation tool.
type source struct {
	t tool.Tool
}

func (s source) Tools() []tool.Tool {
	if s.t == nil {
		return nil
	}
	return []tool.Tool{s.t}
}

func (source) LazyTools() []tool.LazyTool { return nil }

var _ tool.Source = source{}

// Tool implements the agent-callable automation tool.
type Tool struct {
	host Host
}

// New creates the tool over a host.
func New(host Host) *Tool { return &Tool{host: host} }

// Definition implements tool.Tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Manage scheduled tasks (automations). create/update/delete ask "+
			"the user to confirm the change before it is applied; list/get "+
			"are read-only. The task argument carries the target workspace "+
			"and schedule. Returns JSON.",
		message.ToolEnumProperty("action", "string",
			"Action to perform.",
			"create", "update", "delete", "list", "get"),
		message.ToolObjectProperty("task",
			"Task fields (required for create/update/delete).",
			map[string]any{
				"id": message.ToolProperty("id", "string",
					"Task id (required for update/delete)."),
				"name": message.ToolProperty("name", "string",
					"Task name (1-200 chars)."),
				"prompt": message.ToolProperty("prompt", "string",
					"Prompt each run executes."),
				"workspace": message.ToolProperty("workspace", "string",
					"Absolute workspace path the task runs in."),
				"mode": message.ToolProperty("mode", "string",
					"Sandbox mode: workspace, read-only or yolo (default workspace)."),
				"model": message.ToolProperty("model", "string",
					"Model hint (provider/name), empty = default routing."),
				"think": message.ToolProperty("think", "string",
					"Reasoning effort: low, medium or high."),
				"conversation_id": message.ToolProperty("conversation_id",
					"string", "Optional existing session id to reuse; empty = new session per run."),
				"notify": message.ToolProperty("notify", "string",
					"Notification policy: always, failed or never."),
				"enabled": message.ToolProperty("enabled", "boolean",
					"Whether the task is scheduled (default true)."),
				"schedule": message.ToolObjectProperty("schedule",
					"Schedule rule.",
					map[string]any{
						"type": message.ToolEnumProperty("type", "string",
							"Schedule type.",
							"hourly", "daily", "weekdays", "weekly"),
						"interval_hours": message.ToolProperty(
							"interval_hours", "integer",
							"hourly: run every N hours."),
						"interval_weeks": message.ToolProperty(
							"interval_weeks", "integer",
							"weekly: run every N weeks (default 1)."),
						"days": message.ToolArrayProperty("days",
							"Weekday abbreviations (MO..SU).",
							message.Items("string")),
						"time": message.ToolProperty("time", "string",
							"daily/weekdays/weekly: HH:MM wall clock."),
					}),
			}),
	).Required("action").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (t *Tool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute implements tool.Tool.
func (t *Tool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Action string           `json:"action"`
		Task   automations.Task `json:"task"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf(
			"automation: parse arguments: %v", err)
	}
	switch strings.TrimSpace(args.Action) {
	case "list":
		tasks, err := t.host.AutomationsList(ctx)
		if err != nil {
			return "", err
		}
		return jsonString(taskViews(tasks))
	case "get":
		if strings.TrimSpace(args.Task.ID) == "" {
			return "", errdefs.Validationf(
				"automation: get requires task.id")
		}
		task, err := t.host.AutomationsGet(ctx, args.Task.ID)
		if err != nil {
			return "", err
		}
		return jsonString(taskView{}.from(task))
	case "create", "update":
		preview, err := t.host.AutomationsPreview(ctx, args.Action, args.Task)
		if err != nil {
			return "", err
		}
		ok, err := t.confirm(ctx, confirmTitle(args.Action), previewSummary(preview))
		if err != nil {
			return "", err
		}
		if !ok {
			return `{"cancelled":true,"action":"` + args.Action + `"}`, nil
		}
		saved, err := t.host.AutomationsApply(ctx, args.Action, preview)
		if err != nil {
			return "", err
		}
		return jsonString(taskView{}.from(saved))
	case "delete":
		if strings.TrimSpace(args.Task.ID) == "" {
			return "", errdefs.Validationf(
				"automation: delete requires task.id")
		}
		preview, err := t.host.AutomationsPreview(ctx, "delete", args.Task)
		if err != nil {
			return "", err
		}
		ok, err := t.confirm(ctx, "Delete automation?",
			fmt.Sprintf("Delete automation %q (%s)? Its run history will be removed.",
				preview.Name, preview.ID))
		if err != nil {
			return "", err
		}
		if !ok {
			return `{"cancelled":true,"action":"delete"}`, nil
		}
		if _, err := t.host.AutomationsApply(ctx, "delete", preview); err != nil {
			return "", err
		}
		return `{"deleted":true,"id":"` + preview.ID + `"}`, nil
	default:
		return "", errdefs.Validationf(
			"automation: unknown action %q", args.Action)
	}
}

// confirm asks the user to approve the change. In headless/automation
// contexts AskUser fails, which naturally blocks agent-created tasks.
func (t *Tool) confirm(
	ctx context.Context, title, body string,
) (bool, error) {
	host, ok := agent.HostFromContext(ctx)
	if !ok {
		return false, errdefs.NotAvailablef(
			"automation: no host in tool context")
	}
	rawOpts, _ := json.Marshal([]runtime.Option{
		{Label: "Yes", Value: "yes"},
		{Label: "No", Value: "no"},
	})
	reply, err := host.AskUser(ctx, agent.UserPrompt{
		Parts:  []message.Part{message.TextPart{Text: body}},
		Source: "opencraft.automation",
		Metadata: map[string]string{
			runtime.MetaKind:    string(runtime.KindConfirm),
			runtime.MetaTitle:   title,
			runtime.MetaOptions: string(rawOpts),
		},
	})
	if err != nil {
		return false, err
	}
	if reply.Metadata[runtime.MetaStatus] == string(runtime.ReplyCancelled) {
		return false, nil
	}
	return reply.Metadata[runtime.MetaChoice] == "yes", nil
}

func confirmTitle(action string) string {
	if action == "create" {
		return "Create automation?"
	}
	return "Update automation?"
}

// previewSummary renders a compact human-readable preview for the
// confirmation prompt.
func previewSummary(task automations.Task) string {
	model := task.Model
	if model == "" {
		model = "default"
	}
	think := task.Think
	if think == "" {
		think = "default"
	}
	session := "new session per run"
	if task.ConversationID != "" {
		session = "existing session " + task.ConversationID
	}
	prompt := task.Prompt
	if len(prompt) > 200 {
		prompt = prompt[:200] + "…"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\n", task.Name)
	fmt.Fprintf(&b, "prompt: %s\n", prompt)
	fmt.Fprintf(&b, "schedule: %s\n", task.Schedule.Description())
	fmt.Fprintf(&b, "workspace: %s\n", task.Workspace)
	fmt.Fprintf(&b, "mode: %s\n", task.Mode)
	fmt.Fprintf(&b, "model: %s\n", model)
	fmt.Fprintf(&b, "think: %s\n", think)
	fmt.Fprintf(&b, "session: %s\n", session)
	fmt.Fprintf(&b, "notify: %s\n", task.Notify)
	if !task.Enabled {
		b.WriteString("enabled: false (paused)\n")
	}
	return strings.TrimSpace(b.String())
}

// taskView is the JSON wire shape of one task returned to the model.
type taskView struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Prompt         string               `json:"prompt"`
	Schedule       automations.Schedule `json:"schedule"`
	Workspace      string               `json:"workspace"`
	Mode           string               `json:"mode"`
	Model          string               `json:"model"`
	Think          string               `json:"think"`
	ConversationID string               `json:"conversation_id,omitempty"`
	Notify         string               `json:"notify"`
	Enabled        bool                 `json:"enabled"`
	NextRunAt      string               `json:"next_run_at,omitempty"`
}

func (taskView) from(t automations.Task) taskView {
	next := ""
	if !t.NextRunAt.IsZero() {
		next = t.NextRunAt.Format(time.RFC3339)
	}
	return taskView{
		ID:             t.ID,
		Name:           t.Name,
		Prompt:         t.Prompt,
		Schedule:       t.Schedule,
		Workspace:      t.Workspace,
		Mode:           t.Mode,
		Model:          t.Model,
		Think:          t.Think,
		ConversationID: t.ConversationID,
		Notify:         t.Notify,
		Enabled:        t.Enabled,
		NextRunAt:      next,
	}
}

func taskViews(tasks []automations.Task) []taskView {
	out := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskView{}.from(t))
	}
	return out
}

func jsonString(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
