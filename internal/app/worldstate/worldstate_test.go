package worldstate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"

	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/tools/plan"
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
	sess, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
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
	got, err := s.discoverAgents()
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
	got, err := s.discoverAgents()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got, "only here") {
		t.Fatalf("got %q", got)
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
	got, err := s.discoverAgents()
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
	sec, err := svc.permissionsSection("c1")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(sec.Text, "go test") || !contains(sec.Text, "npm install") {
		t.Fatalf("permissions section = %q, want approved prefixes", sec.Text)
	}
	if !contains(sec.Text, "rejected without asking") {
		t.Fatalf("permissions section = %q, want workspace confinement note", sec.Text)
	}

	// Without a provider the approved-prefix line is omitted entirely.
	plain := New(Options{WorkBase: t.TempDir()})
	sec2, err := plain.permissionsSection("c1")
	if err != nil {
		t.Fatal(err)
	}
	if contains(sec2.Text, "Approved command prefixes") {
		t.Fatalf("permissions section = %q, want no approved-prefix line", sec2.Text)
	}
}

func TestPermissionsSectionShowsYOLOForSession(t *testing.T) {
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetSessions(store)

	sec, err := svc.permissionsSection(id)
	if err != nil {
		t.Fatal(err)
	}
	if contains(sec.Text, "yolo") {
		t.Fatalf("workspace session must not show yolo: %q", sec.Text)
	}
	if err := store.SetMode(id, ocsessions.ModeYOLO); err != nil {
		t.Fatal(err)
	}
	sec2, err := svc.permissionsSection(id)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(sec2.Text, "yolo") {
		t.Fatalf("yolo session must show the marker: %q", sec2.Text)
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
	got := svc.memorySections(context.Background(), "c1")
	if len(got) != 2 {
		t.Fatalf("sections = %+v, want summary + raw", got)
	}
	if got[0].ID != "memory_summary" || got[0].Role != "system" || !contains(got[0].Text, "folded summary") {
		t.Fatalf("summary section = %+v", got[0])
	}
	if got[1].ID != "memory_raw" || got[1].Role != string(message.RoleAssistant) || !contains(got[1].Text, "recent raw message") {
		t.Fatalf("raw section = %+v", got[1])
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
		context.Background(), "assistant", "c1", board,
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
	if summary == nil || !contains(summary.Text, "folded summary") {
		t.Fatalf("missing summary section in %+v", sections)
	}
	if rawMsg == nil || rawMsg.Role != string(message.RoleUser) || !contains(rawMsg.Text, "latest user turn") {
		t.Fatalf("missing raw section with role in %+v", sections)
	}
}

func TestRenderToBoardInjectsLatestPlan(t *testing.T) {
	sess := newSessionStore(t)
	store := plan.NewStore(sess)
	if _, err := store.Update("assistant", "c1", plan.UpdatePlanArgs{
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
		context.Background(), "assistant", "c1", board,
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
	if !contains(planSec.Text, "inspect") ||
		!contains(planSec.Text, plan.StatusInProgress) ||
		!contains(planSec.Text, "fix the bug") {
		t.Fatalf("plan section = %q, want checklist with explanation", planSec.Text)
	}

	// An empty store injects no plan section.
	empty := New(Options{WorkBase: t.TempDir()})
	empty.SetSessions(newSessionStore(t))
	board2 := agent.NewBoard()
	if err := empty.RenderToBoard(
		context.Background(), "assistant", "c2", board2,
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
	if _, err := doneStore.Update("assistant", "c3", plan.UpdatePlanArgs{
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
		context.Background(), "assistant", "c3", board3,
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
