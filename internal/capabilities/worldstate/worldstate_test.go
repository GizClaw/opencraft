package worldstate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/plan"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newSessionStore(t *testing.T) *ocsessions.Store {
	t.Helper()
	sess, err := sessionstore.Open(t, filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestDiscoverAgentsRootToCwdWithOverride(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git"), "")
	sub := filepath.Join(root, "a", "b")
	write(t, filepath.Join(root, "AGENTS.md"), "root doc")
	write(t, filepath.Join(sub, "AGENTS.md"), "sub doc")
	write(t, filepath.Join(sub, "AGENTS.override.md"), "override doc")

	s := New(Options{WorkBase: sub})
	got, err := s.discoverAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got, "root doc") {
		t.Fatalf("missing root doc: %q", got)
	}
	if contains(got, "sub doc") {
		t.Fatalf("sub doc must be overridden: %q", got)
	}
	if !contains(got, "override doc") {
		t.Fatalf("missing override doc: %q", got)
	}
}

func TestDiscoverAgentsFallsBackToCwd(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AGENTS.md"), "only here")
	s := New(Options{WorkBase: dir})
	got, err := s.discoverAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got, "only here") {
		t.Fatalf("got %q", got)
	}
}

func TestRenderToBoardRefreshesAgentsMdEachTurn(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git"), "")
	write(t, filepath.Join(root, "AGENTS.md"), "version one")
	svc := New(Options{WorkBase: root})
	ctx := context.Background()
	render := func() string {
		t.Helper()
		board := agent.NewBoard()
		if err := svc.RenderToBoard(
			ctx, "assistant", "s-c1", "hi", nil, board,
		); err != nil {
			t.Fatal(err)
		}
		return board.GetVarString("world.sections")
	}
	if first := render(); !contains(first, "version one") {
		t.Fatalf("first render missing AGENTS.md: %q", first)
	}
	write(t, filepath.Join(root, "AGENTS.md"), "version two")
	second := render()
	if !contains(second, "version two") {
		t.Fatalf("second render still stale: %q", second)
	}
	if contains(second, "version one") {
		t.Fatalf("second render served old AGENTS.md: %q", second)
	}
}

func TestRenderToBoardPlacesAgentsAfterPermissions(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "AGENTS.md"), "project rules")
	svc := New(Options{WorkBase: root})
	board := agent.NewBoard()
	if err := svc.RenderToBoard(
		context.Background(), "assistant", "s-c1", "hi", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	raw := board.GetVarString("world.sections")
	var sections []Section
	if err := json.Unmarshal([]byte(raw), &sections); err != nil {
		t.Fatalf("sections = %q: %v", raw, err)
	}
	if len(sections) != len(baseFragmentOrder)+3 {
		t.Fatalf("sections = %d (%v), want base(%d) + environment + permissions + agents_md",
			len(sections), sectionIDs(sections), len(baseFragmentOrder))
	}
	got := sectionIDs(sections)
	if got[len(baseFragmentOrder)] != "environment" ||
		got[len(baseFragmentOrder)+1] != "permissions" ||
		got[len(baseFragmentOrder)+2] != "agents_md" {
		t.Fatalf("order after base = %v, want environment, permissions, agents_md",
			got[len(baseFragmentOrder):])
	}
	if sections[len(sections)-1].Role != "user" {
		t.Fatalf("agents_md role = %q, want user (user-authored guidance)",
			sections[len(sections)-1].Role)
	}
	for _, sec := range sections {
		if sec.ID == "git" {
			t.Fatalf("git section must not be injected: %+v", sections)
		}
	}
}

func TestRenderToBoardGroupsAllSystemBeforeUserContent(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "AGENTS.md"), "project rules")
	svc := New(Options{WorkBase: root})
	svc.memory = stubMemory{items: []corememory.ContextItem{
		{
			Kind: corememory.ContextSummary,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "folded summary"},
			}},
		},
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleUser,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "raw history"},
			}},
		},
	}}
	board := agent.NewBoard()
	if err := svc.RenderToBoard(
		context.Background(), "assistant", "s-c1", "hi", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	rawBoard := board.GetVarString("world.sections")
	var sections []Section
	if err := json.Unmarshal([]byte(rawBoard), &sections); err != nil {
		t.Fatalf("sections = %q: %v", rawBoard, err)
	}
	assertSystemFirst(t, sections)
	var summaryAt, agentsAt, rawAt = -1, -1, -1
	for i, sec := range sections {
		switch sec.ID {
		case "memory_summary":
			summaryAt = i
		case "agents_md":
			agentsAt = i
		case "memory_raw":
			rawAt = i
		}
	}
	if summaryAt < 0 || agentsAt < 0 || rawAt < 0 ||
		agentsAt >= summaryAt || summaryAt >= rawAt {
		t.Fatalf("order = %v, want agents_md < memory_summary < memory_raw",
			sectionIDs(sections))
	}
}

func TestRenderToBoardFullSectionOrder(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "AGENTS.md"), "project rules")
	writeSkillFile(t, root, "review", "review code and docs")
	svc := skills.NewService(context.Background(), skills.Options{
		Enabled: true, TopN: 5,
	})
	ws := New(Options{WorkBase: root})
	ws.SetSkills(svc)
	ws.memory = stubMemory{items: []corememory.ContextItem{
		{
			Kind: corememory.ContextSummary,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "folded summary"},
			}},
		},
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleUser,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "raw history"},
			}},
		},
	}}
	sess := newSessionStore(t)
	store := plan.NewStore(sess)
	if _, err := store.Update("assistant", "s-c1", plan.UpdatePlanArgs{
		Plan: []plan.PlanItem{
			{Step: "inspect", Status: plan.StatusInProgress},
		},
	}); err != nil {
		t.Fatal(err)
	}
	ws.SetSessions(sess)

	board := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1", "use $review", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	sections := unmarshalSections(t, board)
	assertSystemFirst(t, sections)
	// The cache-stable prefix keeps the harness-owned content and the
	// conversation prefix (folded summary, raw window) in stable-first
	// order; the per-turn tail is asserted below.
	want := []string{
		"agents_md", "memory_summary", "memory_raw",
	}
	pos := -1
	for _, id := range want {
		next := -1
		for i, sec := range sections {
			if sec.ID == id && i > pos {
				next = i
				break
			}
		}
		if next < 0 {
			t.Fatalf("section %q missing or out of order in %v", id, sectionIDs(sections))
		}
		pos = next
	}
	// The per-turn tail (plan, skills list, activated skill) rides with
	// the user's message instead of becoming channel messages: it must
	// not appear in world.sections, and it must all be in the block.
	for _, id := range []string{"plan", "skills", "skill"} {
		for _, sec := range sections {
			if sec.ID == id {
				t.Fatalf("per-turn section %q must not be in world.sections (%v)",
					id, sectionIDs(sections))
			}
		}
	}
	block := board.GetVarString("world.tail_block")
	for _, frag := range []string{
		"inspect",          // plan item
		"## Skills",        // ranked skills list
		"## Skill: review", // activated skill body
	} {
		if !strings.Contains(block, frag) {
			t.Fatalf("tail block is missing %q:\n%s", frag, block)
		}
	}
}

// TestRenderToBoardPrefixIsStableAcrossTurns is the caching contract that
// the stable/volatile split exists for: within one session, a turn that only
// differs by the user's wording must render a byte-identical world.sections,
// so a provider's prompt cache can reuse everything up to the conversation.
// Everything that legitimately changes per turn is expected in
// world.tail_block instead.
func TestRenderToBoardPrefixIsStableAcrossTurns(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "AGENTS.md"), "project rules")
	writeSkillFile(t, root, "review", "review code and docs")
	writeSkillFile(t, root, "plan", "build execution plans")
	svc := skills.NewService(context.Background(), skills.Options{
		Enabled: true, TopN: 5,
	})
	ws := New(Options{WorkBase: root})
	ws.SetSkills(svc)
	ws.memory = stubMemory{items: []corememory.ContextItem{{
		Kind: corememory.ContextSummary,
		Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "folded summary"},
		}},
	}}}
	sess := newSessionStore(t)
	if _, err := plan.NewStore(sess).Update("assistant", "s-c1",
		plan.UpdatePlanArgs{Plan: []plan.PlanItem{
			{Step: "inspect", Status: plan.StatusInProgress},
		}}); err != nil {
		t.Fatal(err)
	}
	ws.SetSessions(sess)

	sectionsA := boardOf(t, ws, "s-c1", "use $review").GetVarString("world.sections")
	blockA := tailBlockOf(t, ws, "s-c1", "use $review")
	sectionsB := boardOf(t, ws, "s-c1", "what does this repo do?").GetVarString("world.sections")
	blockB := tailBlockOf(t, ws, "s-c1", "what does this repo do?")

	if sectionsA != sectionsB {
		t.Fatalf("world.sections changed between two turns of one session:\n--- A\n%s\n--- B\n%s",
			sectionsA, sectionsB)
	}
	if blockA == blockB {
		t.Fatalf("the tail block should differ when the skills ranking does:\n%s", blockA)
	}
}

// TestRenderTailBlock pins the framing rules the block depends on: an empty
// tail stays unset so the node leaves the turn message untouched, and a
// non-empty tail is marked as injected context (never as the user's words).
func TestRenderTailBlock(t *testing.T) {
	if got := renderTailBlock(nil); got != "" {
		t.Fatalf("empty tail = %q, want \"\"", got)
	}
	blank := []Section{newTextSection("plan", message.RoleUser, "   \n")}
	if got := renderTailBlock(blank); got != "" {
		t.Fatalf("blank tail = %q, want \"\"", got)
	}
	got := renderTailBlock([]Section{
		newTextSection("plan", message.RoleUser, "Current plan:\n- [~] inspect (in_progress)"),
		newTextSection("skills", message.RoleUser, "## Skills\n- review: review code"),
	})
	if !strings.HasPrefix(got, tailBlockHeader) ||
		!strings.HasSuffix(got, tailBlockFooter) {
		t.Fatalf("tail block is not framed:\n%s", got)
	}
	if !strings.Contains(got, "Current plan:") ||
		!strings.Contains(got, "## Skills") {
		t.Fatalf("tail block lost a section:\n%s", got)
	}
	if listAt := strings.Index(got, "## Skills"); listAt < strings.Index(got, "Current plan:") {
		t.Fatalf("tail block order = plan last, want plan first:\n%s", got)
	}
}

// TestRenderToBoardClearsAStaleTailBlock pins the reused-board case. A
// resume restores the previous run's board and its vars, so a turn whose
// tail is empty must clear the var: otherwise the last turn's plan and
// skills would be appended to this turn's message as if they were current.
func TestRenderToBoardClearsAStaleTailBlock(t *testing.T) {
	workBase := t.TempDir()
	writeSkillFile(t, workBase, "review", "review code and docs")
	ws := New(Options{WorkBase: workBase})
	ws.SetSkills(skills.NewService(context.Background(), skills.Options{
		Enabled: true,
	}))
	board := agent.NewBoard()
	board.SetVar("world.tail_block",
		"<opencraft-context>\nstale plan\n</opencraft-context>")

	// A turn with no plan and no matching skill has nothing to inject.
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1",
		"zzzzqqqq nothing relevant", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	if got := board.GetVarString("world.tail_block"); got != "" {
		t.Fatalf("stale tail block survived: %q", got)
	}
}

func TestDiscoverAgentsViaWorkspace(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git"), "")
	write(t, filepath.Join(root, "AGENTS.md"), "workspace doc")
	ws, err := NewLocalWorkspaceForTest(root)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{WorkBase: root, Workspace: ws})
	got, err := s.discoverAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got, "workspace doc") {
		t.Fatalf("got %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func strptr(s string) *string { return &s }

// boardOf renders one turn and returns the board the world node reads.
func boardOf(t *testing.T, ws *Service, contextID, prompt string) *agent.Board {
	t.Helper()
	board := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", contextID, prompt, nil, board,
	); err != nil {
		t.Fatal(err)
	}
	return board
}

// tailBlockOf renders one turn and returns the per-turn block the world
// node appends to the user's own message ("" when the turn has none).
func tailBlockOf(t *testing.T, ws *Service, contextID, prompt string) string {
	t.Helper()
	return boardOf(t, ws, contextID, prompt).GetVarString("world.tail_block")
}

type stubPrefixProvider []string

func (s stubPrefixProvider) Rules() []string { return []string(s) }

func TestPermissionsSectionShowsLiveRules(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetPrefixProvider(stubPrefixProvider{"go test", "npm install"})
	sec, err := svc.permissionsSection(context.Background(), "s-c1")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(sec.Content.Text(), "go test") || !contains(sec.Content.Text(), "npm install") {
		t.Fatalf("permissions section = %q, want approved prefixes", sec.Content.Text())
	}
	if !contains(sec.Content.Text(), "rejected without asking") {
		t.Fatalf("permissions section = %q, want workspace confinement note", sec.Content.Text())
	}

	// Without a provider the approved-prefix line is omitted entirely.
	plain := New(Options{WorkBase: t.TempDir()})
	sec2, err := plain.permissionsSection(context.Background(), "s-c1")
	if err != nil {
		t.Fatal(err)
	}
	if contains(sec2.Content.Text(), "Approved command prefixes") {
		t.Fatalf("permissions section = %q, want no approved-prefix line", sec2.Content.Text())
	}
}

func TestPermissionsSectionShowsYOLOForSession(t *testing.T) {
	store, err := sessionstore.Open(t, filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetSessions(store)

	// The yoloonly build resolves every session to yolo up front, so
	// there is no confined state before the mode switch.
	if !profile.YoloOnly() {
		sec, err := svc.permissionsSection(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if contains(sec.Content.Text(), "yolo") {
			t.Fatalf("workspace session must not show yolo: %q", sec.Content.Text())
		}
	}
	if err := store.SetMode(context.Background(), id, ocsessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}
	sec2, err := svc.permissionsSection(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(sec2.Content.Text(), "yolo") {
		t.Fatalf("yolo session must show the marker: %q", sec2.Content.Text())
	}
	for _, forbidden := range []string{
		"Commands outside the approved allowlist",
		"File reads and writes are confined",
	} {
		if contains(sec2.Content.Text(), forbidden) {
			t.Fatalf("yolo permissions section must not claim approvals/file confinement: %q",
				sec2.Content.Text())
		}
	}
}

func TestPermissionsSectionShowsReadOnlyForSession(t *testing.T) {
	if profile.YoloOnly() {
		t.Skip("read-only sessions do not exist in the yoloonly build")
	}
	store, err := sessionstore.Open(t, filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetSessions(store)

	sec, err := svc.permissionsSection(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if contains(sec.Content.Text(), "read-only") {
		t.Fatalf("workspace session must not show read-only: %q", sec.Content.Text())
	}
	if err := store.SetMode(context.Background(), id, ocsessions.ModeReadOnly); err != nil {
		t.Fatal(err)
	}
	sec2, err := svc.permissionsSection(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(sec2.Content.Text(), "read-only") {
		t.Fatalf("read-only session must show the marker: %q", sec2.Content.Text())
	}
}

type stubMemory struct {
	items []corememory.ContextItem
}

func (m stubMemory) Context(
	context.Context,
	corememory.ContextRequest,
) (corememory.ContextResult, error) {
	return corememory.ContextResult{Items: m.items}, nil
}

// replayMemory is a stubMemory that advertises full-replay mode.
type replayMemory struct {
	stubMemory
}

func (replayMemory) ReplayFullHistory() bool { return true }

func TestMemorySectionsIncludeSummariesAndRaw(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	svc.memory = stubMemory{items: []corememory.ContextItem{
		{
			Kind: corememory.ContextSummary,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "folded summary"},
			}},
		},
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "recent raw message"},
			}},
		},
	}}
	got := svc.memorySections(context.Background(), "s-c1")
	if len(got) != 2 {
		t.Fatalf("sections = %+v, want summary + raw", got)
	}
	if got[0].ID != "memory_summary" || got[0].Role != "user" || !contains(got[0].Content.Text(), "folded summary") {
		t.Fatalf("summary section = %+v", got[0])
	}
	if got[1].ID != "memory_raw" || got[1].Role != message.RoleAssistant || !contains(got[1].Content.Text(), "recent raw message") {
		t.Fatalf("raw section = %+v", got[1])
	}
}

// TestMemorySectionsDropsToolTextWithoutPair verifies a text-only
// role=tool row (no ToolResultPart and no matching call in the batch)
// is treated as an orphan and omitted instead of becoming an invalid
// tool message or a misleading user turn.
func TestMemorySectionsDropsToolTextWithoutPair(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	svc.memory = stubMemory{items: []corememory.ContextItem{
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "tool_result: build ok"},
			}},
		},
	}}
	got := svc.memorySections(context.Background(), "s-c1")
	if len(got) != 0 {
		t.Fatalf("sections = %+v, want orphan tool text dropped", got)
	}
}

func TestRenderToBoardInjectsMemorySectionsNoHistory(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	svc.memory = stubMemory{items: []corememory.ContextItem{
		{
			Kind: corememory.ContextSummary,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "folded summary"},
			}},
		},
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleUser,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "latest user turn"},
			}},
		},
	}}
	board := agent.NewBoard()
	if err := svc.RenderToBoard(
		context.Background(), "assistant", "s-c1", "latest user turn", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	raw := board.GetVarString("world.sections")
	var sections []Section
	if err := json.Unmarshal([]byte(raw), &sections); err != nil {
		t.Fatalf("sections = %q: %v", raw, err)
	}
	var summary, rawMsg *Section
	for i := range sections {
		switch sections[i].ID {
		case "memory_summary":
			summary = &sections[i]
		case "memory_raw":
			rawMsg = &sections[i]
		case "history":
			t.Fatalf("history section must not be injected (memory is the single source): %+v", sections[i])
		}
	}
	assertSystemFirst(t, sections)
	if summary == nil || !contains(summary.Content.Text(), "folded summary") {
		t.Fatalf("missing summary section in %+v", sections)
	}
	if rawMsg == nil || rawMsg.Role != message.RoleUser || !contains(rawMsg.Content.Text(), "latest user turn") {
		t.Fatalf("missing raw section with role in %+v", sections)
	}
}

// TestRenderToBoardNormalizesRawToolContext locks the final world
// sections for a dirty raw window: a call without a result must become
// a synthetic aborted tool message, and an orphan result must not leak
// into the model as text.
func TestRenderToBoardNormalizesRawToolContext(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	svc.memory = stubMemory{items: []corememory.ContextItem{
		toolCallItem("c-raw-1"),
		toolResultItem("missing", "orphan output"),
	}}
	board := agent.NewBoard()
	if err := svc.RenderToBoard(
		context.Background(), "assistant", "s-c1", "continue", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	sections := unmarshalSections(t, board)
	var raws []Section
	for _, sec := range sections {
		if sec.ID == "memory_raw" {
			raws = append(raws, sec)
		}
	}
	if len(raws) != 2 {
		t.Fatalf("memory_raw sections = %+v, want call + aborted result", raws)
	}
	if raws[0].Role != message.RoleAssistant {
		t.Fatalf("first raw role = %s, want assistant", raws[0].Role)
	}
	if raws[1].Role != message.RoleTool {
		t.Fatalf("second raw role = %s, want tool", raws[1].Role)
	}
	results := raws[1].ToolResults()
	if len(results) != 1 || results[0].CallID != "c-raw-1" ||
		results[0].Content.Text() != "aborted" {
		t.Fatalf("synthetic result = %+v", results)
	}
	for _, sec := range raws {
		if sec.Content.Text() == "orphan output" {
			t.Fatalf("orphan output leaked into world sections: %+v", raws)
		}
	}
	assertSystemFirst(t, sections)
}

func TestRenderToBoardReplayFullHistory(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	svc.memory = replayMemory{stubMemory: stubMemory{items: []corememory.ContextItem{
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleUser,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "early user turn"},
			}},
		},
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "early assistant turn"},
			}},
		},
	}}}

	board := agent.NewBoard()
	if err := svc.RenderToBoard(
		context.Background(), "assistant", "s-c1", "current turn", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	history := board.GetVarString("world.history")
	for _, want := range []string{"early user turn", "early assistant turn"} {
		if !strings.Contains(history, want) {
			t.Fatalf("world.history missing %q: %s", want, history)
		}
	}

	raw := board.GetVarString("world.sections")
	var sections []Section
	if err := json.Unmarshal([]byte(raw), &sections); err != nil {
		t.Fatalf("sections = %q: %v", raw, err)
	}
	for _, sec := range sections {
		if sec.ID == "memory_raw" || sec.ID == "memory_summary" {
			t.Fatalf("replay mode must not inject memory sections: %+v", sections)
		}
	}
}

// assertSystemFirst fails when a user-role section precedes a later
// system-role section: system context must be grouped first, and all
// user-side content (AGENTS.md, raw history, activated skills) must
// follow as a single trailing block.
func assertSystemFirst(t *testing.T, sections []Section) {
	t.Helper()
	seenUser := false
	for _, sec := range sections {
		if sec.Role == "user" {
			seenUser = true
		} else if sec.Role == "system" && seenUser {
			t.Fatalf("system section %q follows user sections: %+v",
				sec.ID, sections)
		}
	}
}

func TestRenderToBoardInjectsLatestPlan(t *testing.T) {
	sess := newSessionStore(t)
	store := plan.NewStore(sess)
	if _, err := store.Update("assistant", "s-c1", plan.UpdatePlanArgs{
		Explanation: strptr("fix the bug"),
		Plan: []plan.PlanItem{
			{Step: "inspect", Status: plan.StatusInProgress},
			{Step: "implement", Status: plan.StatusPending},
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetSessions(sess)
	// The plan is per-turn state: it rides in the tail block appended to
	// the user's own message, never as a channel message of its own.
	block := tailBlockOf(t, svc, "s-c1", "")
	if !contains(block, "inspect") ||
		!contains(block, plan.StatusInProgress) ||
		!contains(block, "fix the bug") {
		t.Fatalf("tail block = %q, want checklist with explanation", block)
	}
	for _, sec := range unmarshalSections(t, boardOf(t, svc, "s-c1", "")) {
		if sec.ID == "plan" {
			t.Fatal("plan must not be a channel section")
		}
	}

	// An empty store injects no plan section.
	empty := New(Options{WorkBase: t.TempDir()})
	empty.SetSessions(newSessionStore(t))
	board2 := agent.NewBoard()
	if err := empty.RenderToBoard(
		context.Background(), "assistant", "s-c2", "", nil, board2,
	); err != nil {
		t.Fatal(err)
	}
	raw2 := board2.GetVarString("world.sections")
	var sections2 []Section
	if err := json.Unmarshal([]byte(raw2), &sections2); err != nil {
		t.Fatal(err)
	}
	for _, sec := range sections2 {
		if sec.ID == "plan" {
			t.Fatal("empty store must not inject a plan section")
		}
	}

	// A fully completed plan is stale context and must not be injected.
	done := New(Options{WorkBase: t.TempDir()})
	doneSess := newSessionStore(t)
	doneStore := plan.NewStore(doneSess)
	if _, err := doneStore.Update("assistant", "s-c3", plan.UpdatePlanArgs{
		Plan: []plan.PlanItem{
			{Step: "inspect", Status: plan.StatusCompleted},
			{Step: "implement", Status: plan.StatusCompleted},
		},
	}); err != nil {
		t.Fatal(err)
	}
	done.SetSessions(doneSess)
	if got := tailBlockOf(t, done, "s-c3", ""); got != "" {
		t.Fatalf("completed plan must not be injected, tail block = %q", got)
	}
}

// writeSkillFile creates a discoverable skill under workBase.
// writeSkillFile writes one user-level skill into <home>/.agents/skills
// and makes `home` the process HOME so discovery scans it.
func writeSkillFile(t *testing.T, home, name, description string) {
	t.Helper()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".agents", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description +
		"\n---\n\n# " + name + "\nDo the " + name + " thing.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRenderToBoardInjectsRankedSkills(t *testing.T) {
	workBase := t.TempDir()
	writeSkillFile(t, workBase, "review", "review code and docs")
	writeSkillFile(t, workBase, "plan", "build execution plans")
	svc := skills.NewService(context.Background(), skills.Options{
		Enabled: true, TopN: 5,
	})
	ws := New(Options{WorkBase: workBase})
	ws.SetSkills(svc)

	board := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1", "please review the docs", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	sections := unmarshalSections(t, board)
	for _, sec := range sections {
		if sec.ID == "skills" || sec.ID == "skill" {
			t.Fatalf("skills must not be channel sections: %+v", sec)
		}
	}
	// The ranked list is per-turn state and rides in the tail block, which
	// is always user-role content: it is appended to the user's message.
	block := board.GetVarString("world.tail_block")
	if !contains(block, "review") ||
		contains(block, "Do the review thing.") {
		t.Fatalf("tail block = %q, want skill metadata only", block)
	}
	assertSystemFirst(t, sections)
}

func TestRenderToBoardSkipsSkillsWhenNoMatch(t *testing.T) {
	workBase := t.TempDir()
	writeSkillFile(t, workBase, "review", "review code and docs")
	svc := skills.NewService(context.Background(), skills.Options{Enabled: true})
	ws := New(Options{WorkBase: workBase})
	ws.SetSkills(svc)

	board := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1", "zzzzqqqq nothing relevant", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	for _, sec := range unmarshalSections(t, board) {
		if sec.ID == "skills" || sec.ID == "skill" {
			t.Fatalf("no match must not inject skills: %+v", sec)
		}
	}
}

func TestRenderToBoardMentionInjectsFullText(t *testing.T) {
	workBase := t.TempDir()
	writeSkillFile(t, workBase, "review", "review code and docs")
	svc := skills.NewService(context.Background(), skills.Options{Enabled: true})
	ws := New(Options{WorkBase: workBase})
	ws.SetSkills(svc)

	board := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1", "use $review now", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	block := board.GetVarString("world.tail_block")
	if !contains(block, "Do the review thing.") {
		t.Fatalf("mention must inject the full skill body:\n%s", block)
	}
	listAt, fullAt := strings.Index(block, "## Skills"),
		strings.Index(block, "Do the review thing.")
	if listAt < 0 || fullAt <= listAt {
		t.Fatalf("skill activation must follow the skills list:\n%s", block)
	}
}

func TestMentionStagesSkillToCache(t *testing.T) {
	workBase := t.TempDir()
	writeSkillFile(t, workBase, "review", "review code and docs")
	scripts := filepath.Join(workBase, ".agents", "skills", "review", "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "run.sh"),
		[]byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	userDir := t.TempDir()
	svc := skills.NewService(context.Background(), skills.Options{Enabled: true})
	ws := New(Options{WorkBase: workBase, UserDir: userDir})
	ws.SetSkills(svc)

	board := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1", "use $review", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	if block := board.GetVarString("world.tail_block"); !contains(block, "staged copy for execution") {
		t.Fatalf("mention must stage the skill:\n%s", block)
	}
	staged := filepath.Join(userDir, "cache", "staged", "s-c1",
		"review", "scripts", "run.sh")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged script missing: %v", err)
	}
}

func TestModelRequestedActivation(t *testing.T) {
	workBase := t.TempDir()
	writeSkillFile(t, workBase, "review", "review code and docs")
	sess := newSessionStore(t)
	svc := skills.NewService(context.Background(), skills.Options{Enabled: true})

	// The model asks for $review at the end of a turn; the observe
	// hook persists the request.
	obs := &activateObserver{svc: svc, store: sess}
	obs.OnRunEnd(context.Background(),
		agent.Identity{AgentID: "assistant", ConversationID: "s-c1"},
		&agent.Result{
			Status: agent.StatusCompleted,
			Messages: []message.Message{
				message.NewTextMessage(message.RoleAssistant,
					"I'll use $review on the next turn"),
			},
		})

	ws := New(Options{WorkBase: workBase})
	ws.SetSkills(svc)
	ws.SetSessions(sess)

	board := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1", "go ahead", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	block := board.GetVarString("world.tail_block")
	if !contains(block, "requested by the model") ||
		!contains(block, "Do the review thing.") {
		t.Fatalf("model-requested activation missing:\n%s", block)
	}

	// consume-on-read: the next turn injects nothing.
	board2 := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1", "again", nil, board2,
	); err != nil {
		t.Fatal(err)
	}
	if block := board2.GetVarString("world.tail_block"); contains(block, "requested by the model") {
		t.Fatalf("activation must be consumed after one turn:\n%s", block)
	}
}

// TestSubagentContextSkipsActivationStore pins the subagent path: a
// "ctx-..." conversation id addresses no persisted session, so skill
// activation state is not written, not read and not logged as a
// failure. Warnings here used to fire on every subagent turn.
func TestSubagentContextSkipsActivationStore(t *testing.T) {
	workBase := t.TempDir()
	writeSkillFile(t, workBase, "review", "review code and docs")
	sess := newSessionStore(t)
	svc := skills.NewService(context.Background(),
		skills.Options{Enabled: true})
	capture := logcapture.Install(t)

	const contextID = "ctx-0f1e2d3c4b5a6978"
	obs := &activateObserver{svc: svc, store: sess}
	obs.OnRunEnd(context.Background(),
		agent.Identity{AgentID: "assistant", ConversationID: contextID},
		&agent.Result{
			Status: agent.StatusCompleted,
			Messages: []message.Message{
				message.NewTextMessage(message.RoleAssistant, "use $review"),
			},
		})

	ws := New(Options{WorkBase: workBase})
	ws.SetSkills(svc)
	ws.SetSessions(sess)
	if names := ws.consumeActivations(
		context.Background(), "assistant", contextID,
	); names != nil {
		t.Fatalf("subagent activations = %v, want nil", names)
	}
	for _, record := range capture.Records() {
		if strings.Contains(record.Body().AsString(), "skill activations") {
			t.Fatalf("subagent context must not warn: %q (%s)",
				record.Body().AsString(),
				logcapture.Attribute(record, "error.message"))
		}
	}
}

func unmarshalSections(t *testing.T, board *agent.Board) []Section {
	t.Helper()
	raw := board.GetVarString("world.sections")
	var sections []Section
	if err := json.Unmarshal([]byte(raw), &sections); err != nil {
		t.Fatalf("sections = %q: %v", raw, err)
	}
	return sections
}

// countingLifecycle records the usage events one turn writes. The
// embedded lifecycle supplies the read half (no decisions, no
// archives), so these tests pin the write path only.
type countingLifecycle struct {
	skillusage.Lifecycle
	mu     sync.Mutex
	events []skillusage.Event
}

func newCountingLifecycle() *countingLifecycle {
	return &countingLifecycle{Lifecycle: skillusage.EmptyLifecycle()}
}

func (c *countingLifecycle) Empty() bool { return false }

func (c *countingLifecycle) Record(
	_ context.Context, event skillusage.Event,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	return nil
}

func (c *countingLifecycle) snapshot() []skillusage.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]skillusage.Event(nil), c.events...)
}

func eventsByName(events []skillusage.Event) map[string]int {
	out := map[string]int{}
	for _, event := range events {
		out[event.Name]++
	}
	return out
}

// newRecordingWorldState discovers the fixture skills under an isolated
// HOME and wires a usage recorder, so a render writes somewhere
// observable instead of into the empty lifecycle.
func newRecordingWorldState(t *testing.T) (*Service, *skills.Service, *countingLifecycle) {
	t.Helper()
	home := t.TempDir()
	writeSkillFile(t, home, "review", "review code and docs")
	writeSkillFile(t, home, "plan", "build execution plans")
	svc := skills.NewService(context.Background(),
		skills.Options{Enabled: true, TopN: 5})
	recorder := newCountingLifecycle()
	svc.SetLifecycle(recorder, config.SkillLifecycleConfig{
		Enabled: true, StaleAfterDays: 45, MinUses: 3, UsageWindowDays: 90,
	}, t.TempDir())
	ws := New(Options{WorkBase: home})
	ws.SetSkills(svc)
	return ws, svc, recorder
}

// TestRenderTurnRecordsRankedSkills pins the per-turn usage write: every
// skill the turn puts in front of the model counts once, attributed to
// the run it happened in.
func TestRenderTurnRecordsRankedSkills(t *testing.T) {
	ws, _, recorder := newRecordingWorldState(t)
	if err := ws.RenderTurn(context.Background(), agent.Identity{
		AgentID: "assistant", RunID: "run-7", ConversationID: "s-c1",
	}, "please review the docs", nil, agent.NewBoard()); err != nil {
		t.Fatal(err)
	}

	events := recorder.snapshot()
	if len(events) == 0 {
		t.Fatal("a turn that injected skills recorded no usage")
	}
	for _, event := range events {
		if event.RunID != "run-7" || event.ConversationID != "s-c1" {
			t.Fatalf("event %+v is not attributed to the turn", event)
		}
		if event.Scope == "" {
			t.Fatalf("event %+v carries no scope", event)
		}
		if event.UsedAt.IsZero() {
			t.Fatalf("event %+v has no timestamp", event)
		}
		if event.Name == "review" && event.Scope != "user" {
			t.Fatalf("review scope = %q, want the user root it came from",
				event.Scope)
		}
	}
	if counts := eventsByName(events); counts["review"] != 1 {
		t.Fatalf("review counted %d times, want 1 (%+v)", counts["review"], events)
	}
}

// TestRenderTurnCountsAMentionedSkillOnce keeps the counters honest: a
// $mention that also ranks is one skill in front of the model, so it is
// one event, not two.
func TestRenderTurnCountsAMentionedSkillOnce(t *testing.T) {
	ws, _, recorder := newRecordingWorldState(t)
	if err := ws.RenderTurn(context.Background(), agent.Identity{
		AgentID: "assistant", RunID: "run-8", ConversationID: "s-c2",
	}, "use $review on the diff", nil, agent.NewBoard()); err != nil {
		t.Fatal(err)
	}
	if counts := eventsByName(recorder.snapshot()); counts["review"] != 1 {
		t.Fatalf("review counted %d times, want 1", counts["review"])
	}
}

// TestRenderTurnRecordsModelRequestedSkill covers the second activation
// path: the model asks for $plan in one reply, the next turn injects it,
// and the use is recorded then — the request itself is not a use.
func TestRenderTurnRecordsModelRequestedSkill(t *testing.T) {
	ws, svc, recorder := newRecordingWorldState(t)
	sess := newSessionStore(t)
	obs := &activateObserver{svc: svc, store: sess}
	obs.OnRunEnd(context.Background(),
		agent.Identity{AgentID: "assistant", ConversationID: "s-c4"},
		&agent.Result{
			Status: agent.StatusCompleted,
			Messages: []message.Message{
				message.NewTextMessage(message.RoleAssistant,
					"I'll use $plan on the next turn"),
			},
		})
	ws.SetSessions(sess)

	if err := ws.RenderTurn(context.Background(), agent.Identity{
		AgentID: "assistant", RunID: "run-9", ConversationID: "s-c4",
	}, "go ahead", nil, agent.NewBoard()); err != nil {
		t.Fatal(err)
	}
	counts := eventsByName(recorder.snapshot())
	if counts["plan"] != 1 {
		t.Fatalf("model-requested plan counted %d times, want 1 (%v)",
			counts["plan"], counts)
	}
}
