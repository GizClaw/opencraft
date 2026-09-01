// Package plan provides the update_plan tool plus a store that
// survives restarts. Every call submits a full checklist snapshot
// (optional explanation + list of steps with statuses). Multiple
// steps may be in_progress at the same time (e.g. parallel subagent
// work); a plan with several in_progress steps is never rejected.
// The latest snapshot is persisted per session by the session store
// (WriteState/ReadState); this package only owns the plan semantics.
package plan

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/sessions"
)

// UpdatePlanName is the canonical update_plan tool name.
const UpdatePlanName = "update_plan"

// Step statuses.
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
// call replaces the whole snapshot.
type Plan struct {
	Explanation string     `json:"explanation,omitempty"`
	Items       []PlanItem `json:"plan"`
	UpdatedAt   string     `json:"updated_at,omitempty"`
}

// Done reports whether every item is completed. A plan with no items
// is not considered done (update_plan always submits at least one).
func (p Plan) Done() bool {
	if len(p.Items) == 0 {
		return false
	}
	for _, item := range p.Items {
		if item.Status != StatusCompleted {
			return false
		}
	}
	return true
}

// UpdatePlanArgs is the update_plan argument payload.
type UpdatePlanArgs struct {
	Explanation *string    `json:"explanation,omitempty"`
	Plan        []PlanItem `json:"plan"`
}

// Store keeps the latest plan per agent/session pair, persisted by the
// session store as <session>/plans.json keyed by agent. It is safe for
// concurrent use.
type Store struct {
	mu    sync.Mutex
	store *sessions.Store
}

// NewStore creates a plan store over the session store.
func NewStore(store *sessions.Store) *Store {
	return &Store{store: store}
}

// Update validates and replaces the agent/session plan with a new
// snapshot, then persists it. The returned Plan is the stored snapshot.
func (s *Store) Update(agentID, sessionID string, args UpdatePlanArgs) (Plan, error) {
	if err := validate(args); err != nil {
		return Plan{}, err
	}
	p := Plan{UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	if args.Explanation != nil {
		p.Explanation = *args.Explanation
	}
	p.Items = append([]PlanItem(nil), args.Plan...)
	s.mu.Lock()
	defer s.mu.Unlock()
	byAgent := map[string]Plan{}
	if err := s.store.ReadState(sessionID, "plans", &byAgent); err != nil &&
		!os.IsNotExist(err) {
		return Plan{}, err
	}
	byAgent[agentID] = p
	if err := s.store.WriteState(sessionID, "plans", byAgent); err != nil {
		return Plan{}, err
	}
	return p, nil
}

// Latest returns the most recent plan snapshot for the agent/session
// pair.
func (s *Store) Latest(agentID, sessionID string) (Plan, bool) {
	var byAgent map[string]Plan
	if err := s.store.ReadState(sessionID, "plans", &byAgent); err != nil {
		return Plan{}, false
	}
	p, ok := byAgent[agentID]
	return p, ok
}

// KeyFromContext returns the agent + conversation ids of the running
// session from the execution context (flowcraft injects RunInfo into
// the context during graph execution). Tools invoked outside a session
// fall back to a shared DefaultSessionID key.
func KeyFromContext(ctx context.Context) (agentID, sessionID string) {
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		agentID = info.AgentID
		sessionID = info.ConversationID
	}
	if agentID == "" {
		agentID = "default"
	}
	if sessionID == "" {
		sessionID = sessions.DefaultSessionID
	}
	return agentID, sessionID
}

// validate enforces the update_plan contract: plan is required and
// each item needs a step and a valid status. Multiple in_progress
// steps are allowed (parallel work).
func validate(args UpdatePlanArgs) error {
	if len(args.Plan) == 0 {
		return errdefs.Validationf(
			"%s: plan is required", UpdatePlanName)
	}
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
	}
	return nil
}

// decodeArgs parses update_plan arguments and rejects unknown fields
// at both the top level and the item level.
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
			"each with a step and status (pending, in_progress, "+
			"completed). Steps may be marked in_progress in parallel "+
			"when work runs concurrently.",
		message.ToolProperty("explanation", "string",
			"Optional explanation of the plan change."),
		message.ToolArrayProperty("plan", "The list of steps", planItemSchema),
	).Required("plan").DisallowAdditionalProperties().Build()
}

func (updatePlanTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

func (t updatePlanTool) Execute(
	ctx context.Context, arguments string,
) (string, error) {
	args, err := decodeArgs(arguments)
	if err != nil {
		return "", err
	}
	agentID, sessionID := KeyFromContext(ctx)
	if _, err := t.store.Update(agentID, sessionID, args); err != nil {
		return "", err
	}
	return "Plan updated", nil
}
