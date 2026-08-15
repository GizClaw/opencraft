// Package worldstate gathers the per-session world state (AGENTS.md,
// permissions, environment, memory context) into board vars. A graph
// script node renders those vars into the model-facing message list.
package worldstate

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/tools/plan"
)

// Section is one world-state fragment written to the board for the
// graph's world node to render. IDs: agents_md | permissions |
// environment | plan | memory_summary | memory_raw.
type Section struct {
	ID   string `json:"id"`
	Role string `json:"role"` // user | system
	Text string `json:"text"`
}

// Options configures the service.
type Options struct {
	WorkBase          string // sandbox/workspace root (runtime cwd)
	UserDir           string // ~/.opencraft
	CollaborationMode string
	PermissionProfile string
	MaxSessionCache   int
	// MemoryMaxItems / MemoryMaxChars bound the memory context budget
	// (folded summary + raw window) injected per turn. Zero uses the
	// defaults below.
	MemoryMaxItems int
	MemoryMaxChars int
	Workspace         workspace.Workspace // optional; in-root file reads go through it
}

// PrefixProvider supplies the current sandbox allowlist rules. The
// execpolicy manager implements it; the permissions section is rendered
// per turn so mid-session approvals appear immediately.
type PrefixProvider interface {
	Rules() []string
}

// Service gathers and caches world state per conversation.
type Service struct {
	opts     Options
	memory   memory.ContextProvider // optional; set after deploy resolves it
	prefixes PrefixProvider         // optional; live allowlist rules
	plans    *plan.Store            // optional; latest plan injection
	mu       sync.Mutex
	sessions map[string]*sessionState
	order    []string
}

type sessionState struct {
	static []Section // agents_md, permissions, environment
}

// New creates a world-state service.
func New(opts Options) *Service {
	if opts.CollaborationMode == "" {
		opts.CollaborationMode = "default"
	}
	if opts.PermissionProfile == "" {
		opts.PermissionProfile = "workspace"
	}
	if opts.MaxSessionCache <= 0 {
		opts.MaxSessionCache = 64
	}
	if opts.MemoryMaxItems <= 0 {
		opts.MemoryMaxItems = 64
	}
	if opts.MemoryMaxChars <= 0 {
		opts.MemoryMaxChars = 1 << 16 // 64 KiB
	}
	return &Service{
		opts:     opts,
		sessions: make(map[string]*sessionState),
	}
}

// SetMemory wires the memory context provider (resolved by deploy).
func (s *Service) SetMemory(m memory.ContextProvider) { s.memory = m }

// SetPrefixProvider wires the live sandbox allowlist rules source.
func (s *Service) SetPrefixProvider(p PrefixProvider) { s.prefixes = p }

// SetPlans wires the plan store so the latest plan is visible to the
// model on every turn.
func (s *Service) SetPlans(st *plan.Store) { s.plans = st }

// RenderToBoard writes the world state into board vars:
//   - world.sections: static sections (AGENTS.md, permissions,
//     environment), the latest plan, and the memory context (folded
//     summary + raw window) from the memory assembly; always injected
//     since each turn starts with a fresh board
//   - world.workspace_root / world.collaboration_mode /
//     world.permission_profile
func (s *Service) RenderToBoard(
	ctx context.Context, agentID, contextID string, board *agent.Board,
) error {
	st, err := s.session(contextID)
	if err != nil {
		return err
	}

	sections := make([]Section, 0, len(st.static)+64)
	for _, sec := range st.static {
		if sec.Text == "" {
			continue
		}
		sections = append(sections, sec)
	}
	// Permissions are live state: the allowlist grows when the user
	// approves commands, so render it on every turn instead of caching
	// it with the static sections.
	permissions, err := s.permissionsSection()
	if err != nil {
		return err
	}
	if permissions.Text != "" {
		sections = append(sections, permissions)
	}
	if s.plans != nil {
		// Inject only while there is still work: a fully completed
		// plan is stale context, so it is dropped from the prompt.
		if p, ok := s.plans.Latest(agentID, contextID); ok && !p.Done() {
			sections = append(sections, Section{
				ID:   "plan",
				Role: "system",
				Text: renderPlanSection(p),
			})
		}
	}
	if s.memory != nil {
		sections = append(sections, s.memorySections(ctx, contextID)...)
	}

	data, err := json.Marshal(sections)
	if err != nil {
		return err
	}
	board.SetVar("world.sections", string(data))
	board.SetVar("world.workspace_root", s.opts.WorkBase)
	board.SetVar("world.collaboration_mode", s.opts.CollaborationMode)
	board.SetVar("world.permission_profile", s.opts.PermissionProfile)
	return nil
}

func (s *Service) session(contextID string) (*sessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.sessions[contextID]; ok {
		return st, nil
	}
	agents, err := s.agentsSection()
	if err != nil {
		return nil, err
	}
	environment, err := s.environmentSection()
	if err != nil {
		return nil, err
	}
	st := &sessionState{
		static: []Section{
			agents,
			environment,
		},
	}
	s.sessions[contextID] = st
	s.order = append(s.order, contextID)
	if len(s.order) > s.opts.MaxSessionCache {
		delete(s.sessions, s.order[0])
		s.order = s.order[1:]
	}
	return st, nil
}

// memorySections packs the memory assembly's context items into board
// sections. The memory assembly is the single source of conversation
// context: folded summaries render as one system section
// (memory_summary), then the raw window messages render as individual
// sections that keep their user/assistant role. The session archive is
// deliberately NOT injected here: it is a durability store (resume
// listing, usage, full transcript) and injecting it alongside memory
// made every turn re-read the whole archive from disk just to duplicate
// the raw window the memory assembly already returns. The raw window
// (max_raw_messages + preserve_recent, 40 in the default deployment)
// plus the folded summary cover exactly what the old history window
// carried, so dropping the archive injection loses nothing.
func (s *Service) memorySections(ctx context.Context, contextID string) []Section {
	if s.memory == nil {
		return nil
	}
	res, err := s.memory.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "opencraft"},
		ConversationID: contextID,
		Budget: memory.Budget{
			MaxItems: s.opts.MemoryMaxItems,
			MaxChars: s.opts.MemoryMaxChars,
		},
	})
	if err != nil {
		return nil
	}
	sections := make([]Section, 0, len(res.Items))
	for _, item := range res.Items {
		text := item.Content.Text()
		if text == "" {
			continue
		}
		switch item.Kind {
		case memory.ContextSummary:
			sections = append(sections, Section{
				ID:   "memory_summary",
				Role: "system",
				Text: text,
			})
		case memory.ContextRawMessage:
			role := string(item.MessageRole)
			if role == "" {
				role = "user"
			}
			sections = append(sections, Section{
				ID:   "memory_raw",
				Role: role,
				Text: text,
			})
		}
	}
	return sections
}
