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

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/confirm"
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

// emptyHost reports that no desktop automation host is wired in.
type emptyHost struct{}

// EmptyHost returns a no-op automation host for runtimes without a
// desktop automation surface (CLI/headless, tests). It satisfies the
// Host contract and exposes no tools, matching the previous nil-host
// Factory behavior now that the value is injected externally.
func EmptyHost() Host { return emptyHost{} }

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
				"id": schemaProp("string",
					"Task id (required for update/delete)."),
				"name": schemaProp("string",
					"Task name (1-200 chars)."),
				"prompt": schemaProp("string",
					"Prompt each run executes."),
				"workspace": schemaProp("string",
					"Absolute workspace path the task runs in."),
				"mode": schemaProp("string",
					"Sandbox mode: workspace, read-only or yolo (default workspace)."),
				"model": schemaProp("string",
					"Model hint (provider/name), empty = default routing."),
				"think": schemaProp("string",
					"Reasoning effort: low, medium or high."),
				"conversation_id": schemaProp("string",
					"Optional existing session id to reuse; empty = new session per run."),
				"notify": schemaProp("string",
					"Notification policy: always, failed or never."),
				"timeout": schemaProp("string",
					"Per-run limit as a duration (e.g. 15m, 2h); empty = the 15-minute default."),
				"enabled": schemaProp("boolean",
					"Whether the task is scheduled (default true)."),
				"schedule": schemaObject("Schedule rule.",
					map[string]any{
						"type": map[string]any{
							"type":        "string",
							"description": "Schedule type.",
							"enum": []any{
								"hourly", "daily", "weekdays", "weekly",
							},
						},
						"interval_hours": schemaProp("integer",
							"hourly: run every N hours."),
						"interval_weeks": schemaProp("integer",
							"weekly: run every N weeks (default 1)."),
						"days": map[string]any{
							"type":        "array",
							"description": "Weekday abbreviations (MO..SU).",
							"items":       message.Items("string"),
						},
						"time": schemaProp("string",
							"daily/weekdays/weekly: HH:MM wall clock."),
					}),
			}),
	).Required("action").DisallowAdditionalProperties().Build()
}

// schemaObject builds one nested object property, with the same
// raw-map rule as schemaProp.
func schemaObject(description string, properties map[string]any) map[string]any {
	return map[string]any{
		"type":        "object",
		"description": description,
		"properties":  properties,
	}
}

// schemaProp builds one property of the nested task/schedule objects.
// Nested values have to be raw JSON Schema maps: a ToolPropertyDef keeps
// its schema in unexported fields, so one placed below the top level
// marshals as an empty object — the model would be handed a property
// named `timeout` with no type and no description, which is how a field
// the tool documents becomes a field the model cannot fill in.
func schemaProp(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}

// Metadata implements tool.ToolMetadata.
func (t *Tool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute implements tool.Tool.
// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *Tool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *Tool) execute(ctx context.Context, arguments string) (string, error) {
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
	return confirm.Confirm(ctx, title, body)
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
	timeout := task.TimeoutLabel()
	if strings.TrimSpace(task.Timeout) == "" {
		timeout += " (default)"
	}
	fmt.Fprintf(&b, "timeout: %s\n", timeout)
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
	Timeout        string               `json:"timeout,omitempty"`
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
		Timeout:        t.Timeout,
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
