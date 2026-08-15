// Package plan provides the plan and update_plan tools plus an
// in-memory plan store. Plans are runtime-scoped: they survive across
// turns within one runtime but are not yet persisted to disk.
package plan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"
)

const (
	// PlanName is the canonical plan tool name.
	PlanName = "plan"
	// UpdatePlanName is the canonical update_plan tool name.
	UpdatePlanName = "update_plan"
)

// Plan is one immutable snapshot of the agent's plan.
type Plan struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Store keeps the latest plan per runtime. It is safe for concurrent
// use.
type Store struct {
	mu     sync.Mutex
	plans  map[string]Plan
	latest string
}

// NewStore creates an empty plan store.
func NewStore() *Store {
	return &Store{plans: make(map[string]Plan)}
}

// Create stores a new plan and makes it the latest.
func (s *Store) Create(text, focus string) (Plan, error) {
	if strings.TrimSpace(text) == "" {
		return Plan{}, errdefs.Validationf("plan: plan text is required")
	}
	if focus != "" {
		text += "\n\nFocus: " + focus
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p := Plan{
		ID:        newID(),
		Text:      text,
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	s.plans[p.ID] = p
	s.latest = p.ID
	s.mu.Unlock()
	return p, nil
}

// Update replaces text/status of an existing plan. id empty means the
// latest plan; it returns ErrNotFound when no plan exists.
func (s *Store) Update(id, text, status, focus string) (Plan, error) {
	if text == "" && status == "" && focus == "" {
		return Plan{}, errdefs.Validationf(
			"update_plan: at least one of plan/status/focus is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		id = s.latest
	}
	p, ok := s.plans[id]
	if !ok {
		return Plan{}, errdefs.NotFoundf("update_plan: plan %q not found", id)
	}
	if text != "" {
		p.Text = text
	}
	if focus != "" {
		p.Text += "\n\nFocus: " + focus
	}
	if status != "" {
		p.Status = status
	}
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.plans[p.ID] = p
	s.latest = p.ID
	return p, nil
}

// Get returns one plan by id.
func (s *Store) Get(id string) (Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.plans[id]
	return p, ok
}

// Latest returns the most recently created/updated plan.
func (s *Store) Latest() (Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.plans[s.latest]
	return p, ok
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("plan-%d", time.Now().UnixNano())
	}
	return "plan-" + hex.EncodeToString(b[:])
}

// ---------------------------------------------------------------------------
// tools
// ---------------------------------------------------------------------------

// Tool is the plan/update_plan tool pair sharing one store.
type Tool struct {
	store *Store
}

// New creates the plan tools over store. store is required.
func New(store *Store) (*Tool, error) {
	if store == nil {
		return nil, errdefs.Validationf("plan: store is required")
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

// Tools returns the plan and update_plan tools.
func (t *Tool) Tools() []tool.Tool {
	return []tool.Tool{planTool{t.store}, updatePlanTool{t.store}}
}

type planTool struct{ store *Store }

var _ tool.Tool = planTool{}

func (planTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		PlanName,
		"Record the agent's execution plan. Call this at the start of "+
			"a multi-step task so the plan is visible to the user and "+
			"can be revised with update_plan. Returns JSON: "+
			"{plan_id, status, created_at, updated_at}.",
		message.ToolProperty("plan", "string",
			"The full plan text (required): numbered steps, what to "+
				"change, and how you will verify the result."),
		message.ToolProperty("focus", "string",
			"Optional focus instruction used when the plan is later "+
				"updated."),
	).Required("plan").DisallowAdditionalProperties().Build()
}

func (planTool) Metadata() tool.ToolMeta { return tool.ToolMeta{MutatesState: true} }

func (t planTool) Execute(_ context.Context, arguments string) (string, error) {
	var args struct {
		Plan  string `json:"plan"`
		Focus string `json:"focus"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf("%s: parse arguments: %v", PlanName, err)
	}
	p, err := t.store.Create(args.Plan, args.Focus)
	if err != nil {
		return "", err
	}
	return encodePlan(p)
}

type updatePlanTool struct{ store *Store }

var _ tool.Tool = updatePlanTool{}

func (updatePlanTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		UpdatePlanName,
		"Revise the agent's plan: replace its text, change its status, "+
			"or append a focus instruction. Returns JSON: {plan_id, "+
			"status, updated_at}.",
		message.ToolProperty("plan_id", "string",
			"Plan id to update; omit to update the latest plan."),
		message.ToolProperty("plan", "string",
			"New plan text replacing the current one."),
		message.ToolProperty("status", "string",
			"New plan status, e.g. active, completed, cancelled."),
		message.ToolProperty("focus", "string",
			"Focus instruction appended to the plan text."),
	).DisallowAdditionalProperties().Build()
}

func (updatePlanTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

func (t updatePlanTool) Execute(_ context.Context, arguments string) (string, error) {
	var args struct {
		PlanID string `json:"plan_id"`
		Plan   string `json:"plan"`
		Status string `json:"status"`
		Focus  string `json:"focus"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf("%s: parse arguments: %v", UpdatePlanName, err)
	}
	p, err := t.store.Update(args.PlanID, args.Plan, args.Status, args.Focus)
	if err != nil {
		return "", err
	}
	return encodePlan(p)
}

func encodePlan(p Plan) (string, error) {
	payload, err := json.Marshal(p)
	if err != nil {
		return "", errdefs.Internalf("plan: encode result: %v", err)
	}
	return string(payload), nil
}
