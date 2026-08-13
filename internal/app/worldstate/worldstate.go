// Package worldstate gathers the per-session world state (AGENTS.md,
// permissions, environment, memory summary) into board vars. A graph
// script node renders those vars into the model-facing message list.
package worldstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/workspace"
)

// Section is one world-state fragment written to the board for the
// graph's world node to render.
type Section struct {
	ID   string `json:"id"`   // agents_md | permissions | environment | memory_summary
	Role string `json:"role"` // user | system
	Text string `json:"text"`
}

// Options configures the service.
type Options struct {
	WorkBase          string // sandbox/workspace root (runtime cwd)
	UserDir           string // ~/.opencraft
	CollaborationMode string
	PermissionProfile string
	ApprovedPrefixes  []string
	MaxSessionCache   int
	Workspace         workspace.Workspace // optional; in-root file reads go through it
}

// Service gathers and caches world state per conversation.
type Service struct {
	opts     Options
	memory   memory.ContextProvider // optional; set after deploy resolves it
	mu       sync.Mutex
	sessions map[string]*sessionState
	order    []string
}

type sessionState struct {
	static       []Section         // agents_md, permissions, environment
	lastInjected map[string]string // section id -> hash
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
	return &Service{
		opts:     opts,
		sessions: make(map[string]*sessionState),
	}
}

// SetMemory wires the memory context provider (resolved by deploy).
func (s *Service) SetMemory(m memory.ContextProvider) { s.memory = m }

// RenderToBoard writes the world state into board vars:
//   - world.sections: diffed Section list (memory_summary always fresh)
//   - world.workspace_root / world.collaboration_mode /
//     world.permission_profile
func (s *Service) RenderToBoard(ctx context.Context, contextID string, board *agent.Board) error {
	st, err := s.session(contextID)
	if err != nil {
		return err
	}

	sections := make([]Section, 0, len(st.static)+1)
	for _, sec := range st.static {
		if sec.Text == "" {
			continue
		}
		hash := hashOf(sec.Text)
		if st.lastInjected[sec.ID] == hash {
			continue
		}
		sections = append(sections, sec)
		st.lastInjected[sec.ID] = hash
	}
	if s.memory != nil {
		if summary := s.memorySummary(ctx, contextID); summary != "" {
			sections = append(sections, Section{
				ID:   "memory_summary",
				Role: "system",
				Text: summary,
			})
		}
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
	permissions, err := s.permissionsSection()
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
			permissions,
			environment,
		},
		lastInjected: make(map[string]string),
	}
	s.sessions[contextID] = st
	s.order = append(s.order, contextID)
	if len(s.order) > s.opts.MaxSessionCache {
		delete(s.sessions, s.order[0])
		s.order = s.order[1:]
	}
	return st, nil
}

func (s *Service) memorySummary(ctx context.Context, contextID string) string {
	if s.memory == nil {
		return ""
	}
	res, err := s.memory.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "opencraft"},
		ConversationID: contextID,
		Budget:         memory.Budget{MaxItems: 64, MaxChars: 1 << 16},
	})
	if err != nil || len(res.Items) == 0 {
		return ""
	}
	var text string
	for _, item := range res.Items {
		text += item.Content.Text() + "\n"
	}
	return text
}

func hashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
