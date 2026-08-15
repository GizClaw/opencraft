// Package plan provides the update_plan tool plus a store that
// survives restarts. The tool follows codex-rs semantics: every call
// submits a full checklist snapshot (optional explanation + list of
// steps with statuses), at most one step may be in_progress, and the
// latest snapshot is persisted atomically to a JSON file when a path
// is configured.
package plan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"
)

// UpdatePlanName is the canonical update_plan tool name.
const UpdatePlanName = "update_plan"

// Step statuses, mirroring codex-rs StepStatus.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
)

// PlanItem is one checklist entry.
type PlanItem struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

// Plan is one immutable snapshot of the agent's plan. Every update_plan
// call replaces the whole snapshot, matching codex-rs PlanUpdate events.
type Plan struct {
	Explanation string     `json:"explanation,omitempty"`
	Items       []PlanItem `json:"plan"`
	UpdatedAt   string     `json:"updated_at,omitempty"`
}

// UpdatePlanArgs mirrors codex-rs UpdatePlanArgs.
type UpdatePlanArgs struct {
	Explanation *string    `json:"explanation,omitempty"`
	Plan        []PlanItem `json:"plan"`
}

// Store keeps the latest plan per runtime, persisted to path on every
// mutation (empty path keeps the store in-memory only). It is safe for
// concurrent use.
type Store struct {
	mu   sync.Mutex
	plan *Plan
	path string
}

// NewStore creates a plan store. When path is non-empty, the existing
// plan is loaded from it and every mutation is written back
// atomically; a missing or unreadable file starts an empty store.
func NewStore(path string) *Store {
	s := &Store{path: path}
	_ = s.load()
	return s
}

// Update validates and replaces the plan with a new snapshot, then
// persists it. The returned Plan is the stored snapshot.
func (s *Store) Update(args UpdatePlanArgs) (Plan, error) {
	if err := validate(args); err != nil {
		return Plan{}, err
	}
	p := Plan{UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	if args.Explanation != nil {
		p.Explanation = *args.Explanation
	}
	p.Items = append([]PlanItem(nil), args.Plan...)
	s.mu.Lock()
	s.plan = &p
	s.mu.Unlock()
	if err := s.save(); err != nil {
		return Plan{}, err
	}
	return p, nil
}

// Latest returns the most recent plan snapshot.
func (s *Store) Latest() (Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.plan == nil {
		return Plan{}, false
	}
	return *s.plan, true
}

func (s *Store) load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	s.plan = &p
	return nil
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(s.plan, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".plans-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// validate enforces the update_plan contract: plan is required, each
// item needs a step and a valid status, and at most one step may be
// in_progress.
func validate(args UpdatePlanArgs) error {
	if len(args.Plan) == 0 {
		return errdefs.Validationf(
			"%s: plan is required", UpdatePlanName)
	}
	inProgress := 0
	for i, item := range args.Plan {
		if strings.TrimSpace(item.Step) == "" {
			return errdefs.Validationf(
				"%s: plan[%d].step is required", UpdatePlanName, i)
		}
		switch item.Status {
		case StatusPending, StatusInProgress, StatusCompleted:
		default:
			return errdefs.Validationf(
				"%s: plan[%d].status must be one of "+
					"pending, in_progress, completed",
				UpdatePlanName, i)
		}
		if item.Status == StatusInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return errdefs.Validationf(
			"%s: at most one step can be in_progress", UpdatePlanName)
	}
	return nil
}

// decodeArgs parses update_plan arguments and rejects unknown fields
// at both the top level and the item level, mirroring codex-rs's
// deny_unknown_fields.
func decodeArgs(arguments string) (UpdatePlanArgs, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &top); err != nil {
		return UpdatePlanArgs{}, errdefs.Validationf(
			"%s: parse arguments: %v", UpdatePlanName, err)
	}
	for key := range top {
		if key != "explanation" && key != "plan" {
			return UpdatePlanArgs{}, errdefs.Validationf(
				"%s: unknown argument %q", UpdatePlanName, key)
		}
	}
	var args UpdatePlanArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return UpdatePlanArgs{}, errdefs.Validationf(
			"%s: parse arguments: %v", UpdatePlanName, err)
	}
	if raw, ok := top["plan"]; ok {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return UpdatePlanArgs{}, errdefs.Validationf(
				"%s: parse arguments: %v", UpdatePlanName, err)
		}
		for i, item := range items {
			for key := range item {
				if key != "step" && key != "status" {
					return UpdatePlanArgs{}, errdefs.Validationf(
						"%s: plan[%d] has unknown field %q",
						UpdatePlanName, i, key)
				}
			}
		}
	}
	return args, nil
}

// ---------------------------------------------------------------------------
// tool
// ---------------------------------------------------------------------------

// Tool is the update_plan tool sharing one store.
type Tool struct {
	store *Store
}

// New creates the update_plan tool over store. store is required.
func New(store *Store) (*Tool, error) {
	if store == nil {
		return nil, errdefs.Validationf(
			"%s: store is required", UpdatePlanName)
	}
	return &Tool{store: store}, nil
}

// MustNew panics on invalid construction; use in static wiring.
func MustNew(store *Store) *Tool {
	t, err := New(store)
	if err != nil {
		panic(err)
	}
	return t
}

// Tools returns the update_plan tool.
func (t *Tool) Tools() []tool.Tool {
	return []tool.Tool{updatePlanTool{t.store}}
}

type updatePlanTool struct{ store *Store }

var _ tool.Tool = updatePlanTool{}

func (updatePlanTool) Definition() message.ToolDefinition {
	planItemProps := map[string]any{
		"step": map[string]any{
			"type":        "string",
			"description": "The step description.",
		},
		"status": map[string]any{
			"type":        "string",
			"description": "One of: pending, in_progress, completed",
			"enum":        []any{StatusPending, StatusInProgress, StatusCompleted},
		},
	}
	planItemSchema := map[string]any{
		"type":                 "object",
		"properties":           planItemProps,
		"required":             []any{"step", "status"},
		"additionalProperties": false,
	}
	return message.DefineSchema(
		UpdatePlanName,
		"Updates the task plan.\n"+
			"Provide an optional explanation and a list of plan items, "+
			"each with a step and status.\n"+
			"At most one step can be in_progress at a time.",
		message.ToolProperty("explanation", "string",
			"Optional explanation of the plan change."),
		message.ToolArrayProperty("plan", "The list of steps", planItemSchema),
	).Required("plan").DisallowAdditionalProperties().Build()
}

func (updatePlanTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

func (t updatePlanTool) Execute(
	_ context.Context, arguments string,
) (string, error) {
	args, err := decodeArgs(arguments)
	if err != nil {
		return "", err
	}
	if _, err := t.store.Update(args); err != nil {
		return "", err
	}
	return "Plan updated", nil
}
