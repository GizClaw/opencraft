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

	"github.com/GizClaw/flowcraft/core/agent"
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

// UpdatePlanArgs mirrors codex-rs UpdatePlanArgs.
type UpdatePlanArgs struct {
	Explanation *string    `json:"explanation,omitempty"`
	Plan        []PlanItem `json:"plan"`
}

// storeKey identifies one plan: the agent + session pair. Mirroring
// flowcraft's sessions.Key, two agents in the same conversation keep
// separate plans.
type storeKey struct {
	agentID   string
	sessionID string
}

// Store keeps the latest plan per agent/session pair, persisted to
// <root>/<sessionID>/plans.json as one entry per agent on every
// mutation (empty root keeps the store in-memory only). It is safe for
// concurrent use.
type Store struct {
	mu    sync.Mutex
	plans map[storeKey]*Plan
	root  string
}

// NewStore creates a per-session plan store rooted at dir. Existing
// plans are loaded lazily on first access; a missing or unreadable
// file simply means no plan for that session yet.
func NewStore(root string) *Store {
	return &Store{root: root, plans: make(map[storeKey]*Plan)}
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
	key := storeKey{agentID: agentID, sessionID: sessionID}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[key] = &p
	if err := s.saveLocked(sessionID); err != nil {
		return Plan{}, err
	}
	return p, nil
}

// Latest returns the most recent plan snapshot for the agent/session
// pair, loading it from disk on first access.
func (s *Store) Latest(agentID, sessionID string) (Plan, bool) {
	key := storeKey{agentID: agentID, sessionID: sessionID}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.plans[key]; ok {
		return *p, true
	}
	s.loadLocked(sessionID)
	p, ok := s.plans[key]
	if !ok {
		return Plan{}, false
	}
	return *p, true
}

// KeyFromContext returns the agent + conversation ids of the running
// session from the execution context (flowcraft injects RunInfo into
// the context during graph execution). Tools invoked outside a session
// fall back to shared "default" keys.
func KeyFromContext(ctx context.Context) (agentID, sessionID string) {
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		agentID = info.AgentID
		sessionID = info.ConversationID
	}
	if agentID == "" {
		agentID = "default"
	}
	if sessionID == "" {
		sessionID = "default"
	}
	return agentID, sessionID
}

// loadLocked fills the cache with every agent's plan persisted for the
// session, without overwriting entries already in memory. Callers must
// hold the store mutex.
func (s *Store) loadLocked(sessionID string) {
	if s.root == "" {
		return
	}
	byAgent, err := s.readFile(sessionID)
	if err != nil {
		return
	}
	for agentID, p := range byAgent {
		k := storeKey{agentID: agentID, sessionID: sessionID}
		if _, ok := s.plans[k]; ok {
			continue
		}
		pp := p
		s.plans[k] = &pp
	}
}

// saveLocked writes the session's plan file: every agent entry on
// disk is merged with the in-memory state so a second store updating a
// different agent cannot clobber the first. Callers must hold the
// store mutex.
func (s *Store) saveLocked(sessionID string) error {
	if s.root == "" {
		return nil
	}
	byAgent, err := s.readFile(sessionID)
	if err != nil {
		return err
	}
	if byAgent == nil {
		byAgent = make(map[string]Plan)
	}
	for key, p := range s.plans {
		if key.sessionID == sessionID {
			byAgent[key.agentID] = *p
		}
	}
	data, err := json.MarshalIndent(byAgent, "", "  ")
	if err != nil {
		return err
	}
	path := s.pathFor(sessionID)
	dir := filepath.Dir(path)
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
	return os.Rename(tmpName, path)
}

// readFile returns the persisted per-agent plans for a session, or nil
// when no file exists yet.
func (s *Store) readFile(sessionID string) (map[string]Plan, error) {
	if s.root == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.pathFor(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var byAgent map[string]Plan
	if err := json.Unmarshal(data, &byAgent); err != nil {
		return nil, err
	}
	return byAgent, nil
}

// pathFor returns the plan file for a session. Session ids are opaque
// file names; ids that could escape the root fall back to "default".
func (s *Store) pathFor(sessionID string) string {
	if unsafeName(sessionID) {
		sessionID = "default"
	}
	return filepath.Join(s.root, sessionID, "plans.json")
}

func unsafeName(name string) bool {
	return name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`)
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
