// Package plan provides the plan and update_plan tools plus a plan
// store that survives restarts: mutations are persisted atomically to
// a JSON file when a path is configured.
package plan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	// GetPlanName is the canonical get_plan tool name.
	GetPlanName = "get_plan"
	// ListPlansName is the canonical list_plans tool name.
	ListPlansName = "list_plans"
)

// Plan is one immutable snapshot of the agent's plan.
type Plan struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Store keeps the latest plan per runtime, persisted to path on every
// mutation (empty path keeps the store in-memory only). It is safe for
// concurrent use.
type Store struct {
	mu     sync.Mutex
	plans  map[string]Plan
	latest string
	path   string
}

// NewStore creates a plan store. When path is non-empty, existing
// plans are loaded from it and every mutation is written back
// atomically; a missing or unreadable file starts an empty store.
func NewStore(path string) *Store {
	s := &Store{plans: make(map[string]Plan), path: path}
	_ = s.load()
	return s
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
	if err := s.save(); err != nil {
		return Plan{}, err
	}
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
	if err := s.save(); err != nil {
		return Plan{}, err
	}
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

// List returns every stored plan, newest first.
func (s *Store) List() []Plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Plan, 0, len(s.plans))
	for _, p := range s.plans {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

type persistFile struct {
	Latest string          `json:"latest"`
	Plans  map[string]Plan `json:"plans"`
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
	var file persistFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	if file.Plans != nil {
		s.plans = file.Plans
	}
	s.latest = file.Latest
	return nil
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(persistFile{
		Latest: s.latest,
		Plans:  s.plans,
	}, "", "  ")
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
	defer os.Remove(tmpName)
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

// Tool is the plan tool group sharing one store.
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

// Tools returns the plan, update_plan, get_plan, and list_plans tools.
func (t *Tool) Tools() []tool.Tool {
	return []tool.Tool{
		planTool{t.store},
		updatePlanTool{t.store},
		getPlanTool{t.store},
		listPlansTool{t.store},
	}
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

// ---------------------------------------------------------------------------
// get_plan / list_plans
// ---------------------------------------------------------------------------

type getPlanTool struct{ store *Store }

var _ tool.Tool = getPlanTool{}

func (getPlanTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		GetPlanName,
		"Read a stored plan. Pass plan_id to read a specific plan, or "+
			"omit it to read the latest plan. Returns the full plan JSON: "+
			"{plan_id, text, status, created_at, updated_at}.",
		message.ToolProperty("plan_id", "string",
			"Plan id to read; omit to read the latest plan."),
	).DisallowAdditionalProperties().Build()
}

func (getPlanTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

func (t getPlanTool) Execute(_ context.Context, arguments string) (string, error) {
	var args struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf(
			"%s: parse arguments: %v", GetPlanName, err)
	}
	p, ok := t.store.Get(args.PlanID)
	if args.PlanID == "" {
		p, ok = t.store.Latest()
	}
	if !ok {
		return "", errdefs.NotFoundf(
			"%s: no plan found", GetPlanName)
	}
	return encodePlan(p)
}

type listPlansTool struct{ store *Store }

var _ tool.Tool = listPlansTool{}

func (listPlansTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		ListPlansName,
		"List every stored plan, newest first. Returns JSON: "+
			"{plans: [{plan_id, text, status, created_at, updated_at}]}.",
	).DisallowAdditionalProperties().Build()
}

func (listPlansTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

func (t listPlansTool) Execute(_ context.Context, arguments string) (string, error) {
	var args struct{}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf(
			"%s: parse arguments: %v", ListPlansName, err)
	}
	plans := t.store.List()
	if plans == nil {
		plans = []Plan{}
	}
	payload, err := json.Marshal(map[string]any{"plans": plans})
	if err != nil {
		return "", errdefs.Internalf(
			"%s: encode result: %v", ListPlansName, err)
	}
	return string(payload), nil
}

func encodePlan(p Plan) (string, error) {
	payload, err := json.Marshal(p)
	if err != nil {
		return "", errdefs.Internalf("plan: encode result: %v", err)
	}
	return string(payload), nil
}
