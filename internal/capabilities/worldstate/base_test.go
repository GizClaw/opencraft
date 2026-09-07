package worldstate

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
)

func TestInstructionSectionsOrderRolesAndBudget(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	secs := svc.instructionSections(context.Background())
	wantIDs := []string{
		"base_identity",
		"base_work",
		"base_sandbox",
		"base_editing",
		"base_agents",
		"base_special",
		"base_format",
	}
	if len(secs) != len(wantIDs) {
		t.Fatalf("base sections = %d (%v), want %d", len(secs), ids(secs), len(wantIDs))
	}
	total := 0
	for i, sec := range secs {
		if sec.ID != wantIDs[i] {
			t.Fatalf("section %d id = %q, want %q", i, sec.ID, wantIDs[i])
		}
		if sec.Role != "system" {
			t.Fatalf("section %s role = %q, want system", sec.ID, sec.Role)
		}
		if len(sec.Content.Text()) > promptFragmentMaxBytes {
			t.Fatalf("section %s exceeds fragment budget: %d bytes", sec.ID, len(sec.Content.Text()))
		}
		total += len(sec.Content.Text())
	}
	if total > promptInstructionTotalBudget {
		t.Fatalf("base total %d bytes exceeds budget %d", total,
			promptInstructionTotalBudget)
	}
	if !strings.Contains(secs[0].Content.Text(), "You are opencraft") {
		t.Fatalf("identity section = %q", secs[0].Content.Text())
	}

	// The budget covers the widest instruction combo: plan mode plus a
	// personality fragment. The two personalities differ in length, so
	// both are exercised.
	for _, personality := range []string{"friendly", "pragmatic"} {
		combined := New(Options{
			WorkBase:          t.TempDir(),
			CollaborationMode: "plan",
			Personality:       personality,
		})
		combinedTotal := 0
		for _, sec := range combined.instructionSections(context.Background()) {
			combinedTotal += len(sec.Content.Text())
		}
		if combinedTotal > promptInstructionTotalBudget {
			t.Fatalf("combined instructions (%s) %d bytes exceed budget %d",
				personality, combinedTotal, promptInstructionTotalBudget)
		}
	}
}

func TestInstructionGroupRegistryOrder(t *testing.T) {
	want := []string{"base", "mode", "personality"}
	if len(instructionGroupOrder) != len(want) {
		t.Fatalf("registry groups = %d, want %d",
			len(instructionGroupOrder), len(want))
	}
	seen := map[string]bool{}
	for i, spec := range instructionGroupOrder {
		if spec.ID != want[i] {
			t.Fatalf("group %d = %q, want %q", i, spec.ID, want[i])
		}
		if spec.Owner == "" || spec.Render == nil {
			t.Fatalf("group %s missing owner/render: %+v", spec.ID, spec)
		}
		if seen[spec.ID] {
			t.Fatalf("duplicate registry id %q", spec.ID)
		}
		seen[spec.ID] = true
	}
}

func TestBaseSectionsRenderBeforeEnvironment(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
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
	if len(sections) == 0 || sections[0].ID != "base_identity" {
		t.Fatalf("first section = %+v, want base_identity", firstOrEmpty(sections))
	}
	envAt := -1
	for i, sec := range sections {
		if sec.ID == "environment" {
			envAt = i
			break
		}
	}
	if envAt <= len(baseFragmentOrder)-1 {
		t.Fatalf("environment at %d must follow base fragments %d", envAt, len(baseFragmentOrder))
	}
	assertSystemFirst(t, sections)
}

func firstOrEmpty(sections []Section) Section {
	if len(sections) == 0 {
		return Section{}
	}
	return sections[0]
}

func TestBaseEmbeddedFragmentsMatchInventory(t *testing.T) {
	entries, err := fs.ReadDir(templateFS, "templates/base")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(baseFragmentOrder) {
		t.Fatalf("embedded files = %d, want %d", len(entries), len(baseFragmentOrder))
	}
	seen := map[string]bool{}
	for _, frag := range baseFragmentOrder {
		seen[frag.File] = true
	}
	for _, entry := range entries {
		name := "base/" + entry.Name()
		if !seen[name] {
			t.Fatalf("embedded fragment %q missing from baseFragmentOrder", name)
		}
	}
}

func TestModeAndPersonalityEmbeddedFragmentsMatchInventory(t *testing.T) {
	groups := []struct {
		dir      string
		registry map[string]fragment
	}{
		{"modes", modeFragments},
		{"personality", personalityFragments},
	}
	for _, g := range groups {
		t.Run(g.dir, func(t *testing.T) {
			entries, err := fs.ReadDir(templateFS, "templates/"+g.dir)
			if err != nil {
				t.Fatal(err)
			}
			want := make(map[string]fragment, len(g.registry))
			for _, spec := range g.registry {
				want[spec.File] = spec
			}
			if len(entries) != len(want) {
				t.Fatalf("embedded files in %s = %d, want %d",
					g.dir, len(entries), len(want))
			}
			for _, entry := range entries {
				file := g.dir + "/" + entry.Name()
				spec, ok := want[file]
				if !ok {
					t.Fatalf("embedded fragment %q missing from registry", file)
				}
				data, err := templateFS.ReadFile("templates/" + file)
				if err != nil || len(data) > promptFragmentMaxBytes {
					t.Fatalf("fragment %s unreadable or exceeds budget: %d bytes (err %v)",
						file, len(data), err)
				}
				if strings.TrimSpace(string(data)) == "" {
					t.Fatalf("fragment %s is empty", file)
				}
				if spec.ID == "" {
					t.Fatalf("registry entry %s has no section ID", file)
				}
			}
		})
	}
}

func TestPlanModeFragmentInjected(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir(), CollaborationMode: "plan"})
	secs := svc.instructionSections(context.Background())
	base := ids(secs)
	planAt := -1
	for i, id := range base {
		if id == "mode_plan" {
			planAt = i
			break
		}
	}
	if planAt != len(baseFragmentOrder) {
		t.Fatalf("mode_plan at %d, want %d (order %v)", planAt,
			len(baseFragmentOrder), base)
	}
	if !strings.Contains(secs[planAt].Content.Text(), "Plan Mode") {
		t.Fatalf("plan fragment = %q", secs[planAt].Content.Text())
	}

	// Default mode injects no mode fragment.
	def := New(Options{WorkBase: t.TempDir()})
	for _, sec := range def.instructionSections(context.Background()) {
		if sec.ID == "mode_plan" {
			t.Fatalf("default mode must not inject mode_plan: %+v", sec)
		}
	}

	// Unknown modes degrade to default behavior.
	unknown := New(Options{WorkBase: t.TempDir(), CollaborationMode: "research"})
	if got := ids(unknown.instructionSections(context.Background())); len(got) != len(baseFragmentOrder) {
		t.Fatalf("unknown mode sections = %v, want base only", got)
	}
}

func TestPersonalityFragmentInjected(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir(), Personality: "friendly"})
	secs := svc.instructionSections(context.Background())
	got := ids(secs)
	if got[len(got)-1] != "personality_friendly" {
		t.Fatalf("personality must follow base/mode: %v", got)
	}
	if !strings.Contains(secs[len(got)-1].Content.Text(), "warm") {
		t.Fatalf("friendly fragment = %q", secs[len(got)-1].Content.Text())
	}

	prag := New(Options{WorkBase: t.TempDir(), Personality: "pragmatic"})
	if got := ids(prag.instructionSections(context.Background())); got[len(got)-1] != "personality_pragmatic" {
		t.Fatalf("pragmatic sections = %v", got)
	}

	none := New(Options{WorkBase: t.TempDir()})
	for _, sec := range none.instructionSections(context.Background()) {
		if strings.HasPrefix(sec.ID, "personality_") {
			t.Fatalf("no personality configured: %+v", sec)
		}
	}

	unknown := New(Options{WorkBase: t.TempDir(), Personality: "energetic"})
	if got := ids(unknown.instructionSections(context.Background())); len(got) != len(baseFragmentOrder) {
		t.Fatalf("unknown personality sections = %v, want base only", got)
	}
}

func TestBaseSectionsDoNotMentionProjectConfig(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir()})
	for _, sec := range svc.instructionSections(context.Background()) {
		for _, forbidden := range []string{
			"project .opencraft",
			".opencraft/approvals.yaml",
			"project's .opencraft",
		} {
			if strings.Contains(sec.Content.Text(), forbidden) {
				t.Fatalf("section %s mentions forbidden project config %q:\n%s",
					sec.ID, forbidden, sec.Content.Text())
			}
		}
	}
}
