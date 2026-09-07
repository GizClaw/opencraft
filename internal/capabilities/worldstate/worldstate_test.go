package worldstate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/plan"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
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
			len(sections), ids(sections), len(baseFragmentOrder))
	}
	got := ids(sections)
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
			ids(sections))
	}
}

func TestRenderToBoardFullSectionOrder(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "AGENTS.md"), "project rules")
	writeSkillFile(t, root, "review", "review code and docs")
	svc := skills.NewService(context.Background(), skills.Options{
		WorkBase: root, Enabled: true, TopN: 5,
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
	want := []string{
		"agents_md", "memory_summary", "plan", "skills", "skill", "memory_raw",
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
			t.Fatalf("section %q missing or out of order in %v", id, ids(sections))
		}
		pos = next
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
	board := agent.NewBoard()
	if err := svc.RenderToBoard(
		context.Background(), "assistant", "s-c1", "", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	raw := board.GetVarString("world.sections")
	var sections []Section
	if err := json.Unmarshal([]byte(raw), &sections); err != nil {
		t.Fatalf("sections = %q: %v", raw, err)
	}
	var planSec *Section
	for i := range sections {
		if sections[i].ID == "plan" {
			planSec = &sections[i]
			break
		}
	}
	if planSec == nil {
		t.Fatalf("no plan section in %+v", sections)
	}
	if planSec.Role != "user" {
		t.Fatalf("plan role = %q, want user (plan is user-side state)", planSec.Role)
	}
	if !contains(planSec.Content.Text(), "inspect") ||
		!contains(planSec.Content.Text(), plan.StatusInProgress) ||
		!contains(planSec.Content.Text(), "fix the bug") {
		t.Fatalf("plan section = %q, want checklist with explanation", planSec.Content.Text())
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
	board3 := agent.NewBoard()
	if err := done.RenderToBoard(
		context.Background(), "assistant", "s-c3", "", nil, board3,
	); err != nil {
		t.Fatal(err)
	}
	raw3 := board3.GetVarString("world.sections")
	var sections3 []Section
	if err := json.Unmarshal([]byte(raw3), &sections3); err != nil {
		t.Fatal(err)
	}
	for _, sec := range sections3 {
		if sec.ID == "plan" {
			t.Fatalf("completed plan must not be injected: %+v", sections3)
		}
	}
}

// writeSkillFile creates a discoverable skill under workBase.
func writeSkillFile(t *testing.T, workBase, name, description string) {
	t.Helper()
	dir := filepath.Join(workBase, ".agents", "skills", name)
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
		WorkBase: workBase, Enabled: true, TopN: 5,
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
	var skillsSec *Section
	for i := range sections {
		if sections[i].ID == "skills" {
			skillsSec = &sections[i]
		}
		if sections[i].ID == "skill" {
			t.Fatal("no mention, no full-text skill section expected")
		}
	}
	if skillsSec == nil {
		t.Fatalf("no skills section in %+v", sections)
	}
	if skillsSec.Role != "user" {
		t.Fatalf("skills list role = %q, want user (skill content is not system rules)",
			skillsSec.Role)
	}
	if !contains(skillsSec.Content.Text(), "review") ||
		contains(skillsSec.Content.Text(), "Do the review thing.") {
		t.Fatalf("skills section = %q, want metadata only", skillsSec.Content.Text())
	}
	assertSystemFirst(t, sections)
}

func TestRenderToBoardSkipsSkillsWhenNoMatch(t *testing.T) {
	workBase := t.TempDir()
	writeSkillFile(t, workBase, "review", "review code and docs")
	svc := skills.NewService(context.Background(), skills.Options{WorkBase: workBase, Enabled: true})
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
	svc := skills.NewService(context.Background(), skills.Options{WorkBase: workBase, Enabled: true})
	ws := New(Options{WorkBase: workBase})
	ws.SetSkills(svc)

	board := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1", "use $review now", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	var list, full *Section
	listAt, fullAt := -1, -1
	sections := unmarshalSections(t, board)
	for i, sec := range sections {
		switch sec.ID {
		case "skills":
			list, listAt = &sec, i
		case "skill":
			full, fullAt = &sec, i
		}
	}
	if full == nil {
		t.Fatalf("mention must inject a full-text skill section")
	}
	if full.Role != "user" || !contains(full.Content.Text(), "Do the review thing.") {
		t.Fatalf("skill section = %+v, want user role with full body", full)
	}
	if list == nil || list.Role != "user" {
		t.Fatalf("skills list = %+v, want user role next to its activation", list)
	}
	if fullAt <= listAt {
		t.Fatalf("skill activation (%d) must follow the skills list (%d): %v",
			fullAt, listAt, ids(sections))
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
	svc := skills.NewService(context.Background(), skills.Options{WorkBase: workBase, Enabled: true})
	ws := New(Options{WorkBase: workBase, UserDir: userDir})
	ws.SetSkills(svc)

	board := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1", "use $review", nil, board,
	); err != nil {
		t.Fatal(err)
	}
	var full *Section
	for _, sec := range unmarshalSections(t, board) {
		if sec.ID == "skill" {
			full = &sec
		}
	}
	if full == nil || !contains(full.Content.Text(), "staged copy for execution") {
		t.Fatalf("mention must stage the skill: %+v", full)
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
	svc := skills.NewService(context.Background(), skills.Options{WorkBase: workBase, Enabled: true})

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
	var injected *Section
	for _, sec := range unmarshalSections(t, board) {
		if sec.ID == "skill" && contains(sec.Content.Text(), "requested by the model") {
			injected = &sec
		}
	}
	if injected == nil || !contains(injected.Content.Text(), "Do the review thing.") {
		t.Fatalf("model-requested activation missing: %+v",
			unmarshalSections(t, board))
	}

	// consume-on-read: the next turn injects nothing.
	board2 := agent.NewBoard()
	if err := ws.RenderToBoard(
		context.Background(), "assistant", "s-c1", "again", nil, board2,
	); err != nil {
		t.Fatal(err)
	}
	for _, sec := range unmarshalSections(t, board2) {
		if sec.ID == "skill" && contains(sec.Content.Text(), "requested by the model") {
			t.Fatalf("activation must be consumed after one turn: %+v", sec)
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
