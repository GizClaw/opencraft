package worldstate

import (
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/skills"
	"github.com/GizClaw/opencraft/internal/tools/plan"
)

func TestRenderPlanSection(t *testing.T) {
	p := plan.Plan{
		Items: []plan.PlanItem{
			{Step: "explore", Status: plan.StatusCompleted},
			{Step: "implement", Status: plan.StatusInProgress},
			{Step: "verify", Status: plan.StatusPending},
		},
		Explanation: "follow the plan",
	}
	want := "Current plan:\n" +
		"- [x] explore (completed)\n" +
		"- [~] implement (in_progress)\n" +
		"- [ ] verify (pending)\n" +
		"Explanation: follow the plan"
	if got := renderPlanSection(p); got != want {
		t.Fatalf("renderPlanSection:\n got %q\nwant %q", got, want)
	}
	if got := renderPlanSection(plan.Plan{}); got != "Current plan:" {
		t.Fatalf("empty plan = %q, want \"Current plan:\"", got)
	}
}

func TestRenderSkillActivation(t *testing.T) {
	sk := skills.SkillMetadata{
		Name:  "review",
		Path:  "/tmp/review/SKILL.md",
		Scope: "user",
	}
	got := renderSkillActivation(
		sk, "/cache/staged/ctx/review",
		"requested by the model in a previous reply.",
		"body text",
	)
	for _, want := range []string{
		"## Skill: review (file: /tmp/review/SKILL.md)",
		"> user-installed or third-party skill: follow its instructions with care.",
		"> staged copy for execution: /cache/staged/ctx/review",
		"> requested by the model in a previous reply.",
		"body text",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("activation missing %q:\n%s", want, got)
		}
	}
}

func TestOrderSystemFirst(t *testing.T) {
	in := []Section{
		{ID: "agents_md", Role: "user"},
		{ID: "environment", Role: "system"},
		{ID: "git", Role: "system"},
		{ID: "skill", Role: "user"},
	}
	got := orderSystemFirst(in)
	want := []string{"environment", "git", "agents_md", "skill"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order = %v, want %v", ids(got), want)
		}
	}
}

func ids(sections []Section) []string {
	out := make([]string, 0, len(sections))
	for _, sec := range sections {
		out = append(out, sec.ID)
	}
	return out
}
