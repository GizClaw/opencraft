// Package worldstate gathers the per-session world state (AGENTS.md,
// permissions, environment, memory context) into board vars. A graph
// script node renders those vars into the model-facing message list.
package worldstate

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/workspace"
	"go.opentelemetry.io/otel/log"

	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/skills"
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
	Workspace      workspace.Workspace // optional; in-root file reads go through it
	Skills         *skills.Service     // optional; per-turn dynamic skill injection
}

// PrefixProvider supplies the current sandbox allowlist rules. The
// execpolicy manager implements it; the permissions section is rendered
// per turn so mid-session approvals appear immediately.
type PrefixProvider interface {
	Rules() []string
}

// Service gathers and caches world state per conversation.
type Service struct {
	opts         Options
	memory       memory.ContextProvider // optional; set after deploy resolves it
	prefixes     PrefixProvider         // optional; live allowlist rules
	sessionStore *ocsessions.Store      // optional; per-session sandbox mode
	mu           sync.Mutex
	sessions     map[string]*sessionState
	order        []string
}

type sessionState struct {
	static []Section // agents_md, environment (permissions/git/plan are per-turn)
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

// SetSkills wires the shared skills registry (resolved by deploy).
func (s *Service) SetSkills(sk *skills.Service) { s.opts.Skills = sk }

// SetPrefixProvider wires the live sandbox allowlist rules source.
func (s *Service) SetPrefixProvider(p PrefixProvider) { s.prefixes = p }

// SetSessions wires the session store so the permissions section can
// report whether the current session is running unconfined (YOLO).
func (s *Service) SetSessions(st *ocsessions.Store) { s.sessionStore = st }

// RenderToBoard writes the world state into board vars:
//   - world.sections: system-role context first (environment,
//     permissions, git, plan, memory summary, skills list), then
//     user-role context (AGENTS.md, raw memory, activated skills)
//     immediately before the user's own message; always injected since
//     each turn starts with a fresh board
//   - world.workspace_root / world.collaboration_mode /
//     world.permission_profile
func (s *Service) RenderToBoard(
	ctx context.Context,
	agentID, contextID, reqText string,
	extras []Section,
	board *agent.Board,
) error {
	st, err := s.session(ctx, contextID)
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
	permissions, err := s.permissionsSection(ctx, contextID)
	if err != nil {
		return err
	}
	if permissions.Text != "" {
		sections = append(sections, permissions)
	}
	if sec := s.gitSection(ctx); sec.Text != "" {
		sections = append(sections, sec)
	}
	if s.sessionStore != nil {
		// Inject only while there is still work: a fully completed
		// plan is stale context, so it is dropped from the prompt.
		if p, ok := plan.NewStore(s.sessionStore).Latest(
			agentID, contextID,
		); ok && !p.Done() {
			sections = append(sections, Section{
				ID:   "plan",
				Role: "system",
				Text: renderPlanSection(p),
			})
		}
	}
	if s.memory != nil {
		if rp, ok := s.memory.(interface {
			ReplayFullHistory() bool
		}); ok && rp.ReplayFullHistory() {
			// Full-history replay: the graph's world node prepends the
			// history right after the world sections, and the compact
			// node owns folding when the model window is exceeded.
			if history := s.replayHistory(ctx, contextID); len(history) > 0 {
				if data, err := json.Marshal(history); err == nil {
					board.SetVar("world.history", string(data))
				}
			}
		} else {
			sections = append(sections, s.memorySections(ctx, contextID)...)
		}
	}
	if s.opts.Skills != nil && s.opts.Skills.Enabled() {
		sections = append(sections,
			s.skillsSections(ctx, agentID, contextID, reqText)...)
	}
	sections = append(sections, extras...)
	// Group user-role context after system-role context: AGENTS.md and
	// activated skills are user-side instructions, not opencraft rules,
	// so they read as "external input" right before the real user
	// message instead of being interleaved among the system sections.
	sections = orderSystemFirst(sections)

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

// orderSystemFirst stably partitions sections so every system-role
// section precedes every user-role section, preserving relative order
// within each group.
func orderSystemFirst(sections []Section) []Section {
	sys := make([]Section, 0, len(sections))
	user := make([]Section, 0, len(sections))
	for _, sec := range sections {
		if sec.Role == "user" {
			user = append(user, sec)
		} else {
			sys = append(sys, sec)
		}
	}
	return append(sys, user...)
}

// skillsSections renders the per-turn skills list (top-N by BM25 over
// the user input, plus any $mention) and injects the full SKILL.md
// body for explicitly mentioned skills. Rendered every turn like the
// permissions section, never cached in sessionState.static.
func (s *Service) skillsSections(
	ctx context.Context,
	agentID, contextID, reqText string,
) []Section {
	svc := s.opts.Skills
	mentioned := svc.Mentioned(reqText)
	modelRequested := s.consumeActivations(agentID, contextID)
	scored := svc.RankScored(reqText, svc.TopN(), svc.MinScore())
	ranked := make([]skills.SkillMetadata, 0, len(scored))
	for _, sc := range scored {
		ranked = append(ranked, sc.Skill)
	}
	list := mergeSkillLists(mentioned, ranked)
	var out []Section
	if len(list) > 0 {
		out = append(out, Section{
			ID:   "skills",
			Role: "system",
			Text: skills.RenderSection(list),
		})
		// Never log the user's message text: reqText can contain
		// anything the user typed. Only metadata is emitted.
		attrs := []log.KeyValue{
			log.Int("count", len(list)),
		}
		for i, sc := range scored {
			attrs = append(attrs, log.String(
				fmt.Sprintf("rank_%d", i+1),
				fmt.Sprintf("%s=%.3f", sc.Skill.Name, sc.Score)))
		}
		telemetry.Info(ctx, "skills: ranked list injected", attrs...)
	}
	for _, sk := range mentioned {
		_, content, err := svc.ReadFull(sk.Name)
		if err != nil {
			out = append(out, Section{
				ID:   "skill",
				Role: "user",
				Text: renderSkillActivation(
					sk, "", "", "(load failed: "+err.Error()+")"),
			})
			continue
		}
		out = append(out, Section{
			ID:   "skill",
			Role: "user",
			Text: renderSkillActivation(
				sk, s.stageSkill(sk, contextID), "", content),
		})
	}
	for _, name := range modelRequested {
		sk, content, err := svc.ReadFull(name)
		if err != nil {
			continue
		}
		out = append(out, Section{
			ID:   "skill",
			Role: "user",
			Text: renderSkillActivation(
				sk,
				s.stageSkill(sk, contextID),
				"requested by the model in a previous reply.",
				content,
			),
		})
		telemetry.Info(ctx, "skills: model-requested activation injected",
			log.String("skill", sk.Name))
	}
	return out
}

// renderSkillActivation annotates an activated skill with its scope
// and a trust warning for user-installed / third-party skills (D12),
// then appends the body (SKILL.md content, or a failure message).
func renderSkillActivation(
	sk skills.SkillMetadata,
	staged, note, body string,
) string {
	out, err := render(skillActivTmpl, skillActivationData{
		Name:      sk.Name,
		Path:      sk.Path,
		Untrusted: sk.Scope != "repo" && sk.Scope != "builtin",
		Staged:    staged,
		Note:      note,
		Body:      body,
	})
	if err != nil {
		return ""
	}
	return strings.TrimRight(out, "\n")
}

// stageSkill copies an activated skill into the sandbox-writable
// cache (OPEN_CRAFT_DATA_DIR/cache/staged/<contextID>/<name>) so its
// scripts are executable under exec even when the skill root itself
// is outside the workspace. Builtins ship no files and are skipped.
func (s *Service) stageSkill(sk skills.SkillMetadata, contextID string) string {
	if sk.Scope == "builtin" || s.opts.UserDir == "" {
		return ""
	}
	root, err := s.opts.Skills.Stage(sk,
		filepath.Join(s.opts.UserDir, "cache", "staged", contextID))
	if err != nil {
		return ""
	}
	return root
}

// mergeSkillLists keeps mentioned skills first (mention order), then
// ranked skills, de-duplicating by path.
func mergeSkillLists(mentioned, ranked []skills.SkillMetadata) []skills.SkillMetadata {
	seen := map[string]bool{}
	var out []skills.SkillMetadata
	for _, sk := range append(append([]skills.SkillMetadata(nil), mentioned...), ranked...) {
		if seen[sk.Path] {
			continue
		}
		seen[sk.Path] = true
		out = append(out, sk)
	}
	return out
}

func (s *Service) session(ctx context.Context, contextID string) (*sessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.sessions[contextID]; ok {
		return st, nil
	}
	agents, err := s.agentsSection(ctx)
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
			switch role {
			case "user", "assistant":
				// keep
			case "tool":
				// Tool results are persisted as role=tool items, but the
				// provider wire format requires a role=tool message to
				// carry a tool_call_id paired with a preceding assistant
				// call. Rendered as plain context, they read as
				// user-supplied material, so inject them as user.
				role = "user"
			default:
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

// historyMessage is one replayed conversation message handed to the
// graph's world node via the world.history board var.
type historyMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// replayHistory returns the full persisted conversation as plain-text
// messages in chronological order. An unlimited budget is requested on
// purpose: the graph compact node decides when to fold based on the
// model's input window. Tool results are rendered as user-role text,
// matching the memory raw-window mapping.
func (s *Service) replayHistory(
	ctx context.Context,
	contextID string,
) []historyMessage {
	res, err := s.memory.Context(ctx, memory.ContextRequest{
		Scope:          memory.Scope{RuntimeID: "opencraft"},
		ConversationID: contextID,
		Budget:         memory.Budget{},
	})
	if err != nil {
		return nil
	}
	out := make([]historyMessage, 0, len(res.Items))
	for _, item := range res.Items {
		if item.Kind != memory.ContextRawMessage {
			continue
		}
		text := item.Content.Text()
		if text == "" {
			continue
		}
		role := string(item.MessageRole)
		if role != "user" && role != "assistant" {
			role = "user"
		}
		out = append(out, historyMessage{Role: role, Text: text})
	}
	return out
}
