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

type stubPrefixProvider []string

func (s stubPrefixProvider) Rules() []string { return []string(s) }

func TestPermissionsSectionShowsLiveRules(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetPrefixProvider(stubPrefixProvider{"go test", "npm install"})
	sec, err := svc.permissionsSection()
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
	sec2, err := plain.permissionsSection()
	if err != nil {
		t.Fatal(err)
	}
	if contains(sec2.Text, "Approved command prefixes") {
		t.Fatalf("permissions section = %q, want no approved-prefix line", sec2.Text)
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

func TestMemorySummarySkipsRawMessages(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	svc.memory = stubMemory{items: []corememory.ContextItem{
		{
			Kind: corememory.ContextSummary,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "folded summary"},
			}},
		},
		{
			Kind: corememory.ContextRawMessage,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "recent raw message"},
			}},
		},
	}}
	got := svc.memorySummary(context.Background(), "c1")
	if !contains(got, "folded summary") {
		t.Fatalf("memory summary = %q, want folded content", got)
	}
	if contains(got, "recent raw message") {
		t.Fatalf("memory summary = %q, must exclude raw messages (history carries them)", got)
	}
}

func TestRenderToBoardInjectsLatestPlan(t *testing.T) {
	store := plan.NewStore(filepath.Join(t.TempDir(), "plans.json"))
	p, err := store.Create("1. inspect\n2. implement", "")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Options{WorkBase: t.TempDir()})
	svc.SetPlans(store)
	board := agent.NewBoard()
	if err := svc.RenderToBoard(context.Background(), "c1", board); err != nil {
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
	if !contains(planSec.Text, p.ID) || !contains(planSec.Text, "inspect") {
		t.Fatalf("plan section = %q, want id and text", planSec.Text)
	}

	// An empty store injects no plan section.
	empty := New(Options{WorkBase: t.TempDir()})
	empty.SetPlans(plan.NewStore(filepath.Join(t.TempDir(), "empty.json")))
	board2 := agent.NewBoard()
	if err := empty.RenderToBoard(context.Background(), "c2", board2); err != nil {
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
}
