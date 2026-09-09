package pet

import (
	"testing"
	"time"
)

func streamEvent(
	agentID, runID, conversationID, partType, toolName string,
	isError bool, text string,
) map[string]any {
	part := map[string]any{"type": partType}
	switch partType {
	case "text", "reasoning":
		part["text"] = text
	case "tool_call":
		part["call"] = map[string]any{"id": "call-1", "name": toolName}
	case "tool_result":
		part["result"] = map[string]any{
			"call_id":  "call-1",
			"is_error": isError,
		}
	}
	return map[string]any{
		"agent_id":        agentID,
		"run_id":          runID,
		"conversation_id": conversationID,
		"delta": map[string]any{
			"type": "part",
			"part": part,
		},
	}
}

func turnEndEvent(agentID, runID, conversationID, status, errText string) map[string]any {
	return map[string]any{
		"agent_id":        agentID,
		"run_id":          runID,
		"conversation_id": conversationID,
		"status":          status,
		"error":           errText,
	}
}

func interactEvent(runID, conversationID string) map[string]any {
	return map[string]any{
		"id":              "prompt-1",
		"run_id":          runID,
		"conversation_id": conversationID,
		"kind":            "confirm",
		"title":           "Approve?",
	}
}

func TestClassifyPetTool(t *testing.T) {
	cases := []struct {
		name string
		want PetToolCategory
	}{
		{"exec_command", PetToolCategoryExec},
		{"exec_session", PetToolCategoryExec},
		{"read_file", PetToolCategoryFile},
		{"apply_patch", PetToolCategoryFile},
		{"web_fetch", PetToolCategoryWeb},
		{"generate_image", PetToolCategoryGenerate},
		{"skill_install", PetToolCategorySkill},
		{"update_plan", PetToolCategoryPlan},
		{"ask_user", PetToolCategoryAsk},
		{"create_agent", PetToolCategoryDelegate},
		{"mcp_custom_call", PetToolCategoryOther},
	}
	for _, tc := range cases {
		if got := classifyPetTool(tc.name); got != tc.want {
			t.Errorf("classifyPetTool(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFeedPhaseTransitions(t *testing.T) {
	feed := NewPetActivityFeed("assistant")

	act := feed.OnEvent("stream", streamEvent(
		"", "r-1", "c-1", "reasoning", "", false, "let me think"))
	if act == nil {
		t.Fatal("first reasoning event must emit an activity")
	}
	if act.AgentID != "assistant" || act.Phase != PetPhaseThinking {
		t.Fatalf("reasoning activity = %+v", act)
	}
	if act.ConversationID != "c-1" || act.RunID != "r-1" {
		t.Fatalf("activity lost run identity: %+v", act)
	}

	// A repeated reasoning delta must not re-emit.
	if got := feed.OnEvent("stream", streamEvent(
		"", "r-1", "c-1", "reasoning", "", false, "more thinking")); got != nil {
		t.Fatalf("repeated reasoning re-emitted: %+v", got)
	}

	act = feed.OnEvent("stream", streamEvent(
		"", "r-1", "c-1", "tool_call", "apply_patch", false, ""))
	if act == nil || act.Phase != PetPhaseTool {
		t.Fatalf("tool activity = %+v", act)
	}
	if act.Tool == nil || act.Tool.Name != "apply_patch" ||
		act.Tool.Category != PetToolCategoryFile {
		t.Fatalf("tool signal = %+v", act.Tool)
	}

	act = feed.OnEvent("stream", streamEvent(
		"", "r-1", "c-1", "text", "", false, "answer!"))
	if act == nil || act.Phase != PetPhaseAnswering {
		t.Fatalf("text activity = %+v", act)
	}

	act = feed.OnEvent("turn_end", turnEndEvent(
		"", "r-1", "c-1", "completed", ""))
	if act == nil || act.Phase != PetPhaseDone {
		t.Fatalf("completed activity = %+v", act)
	}
}

func TestFeedToolErrorRetainsTool(t *testing.T) {
	feed := NewPetActivityFeed("assistant")
	if got := feed.OnEvent("stream", streamEvent(
		"assistant", "r-1", "c-1", "tool_call", "exec_command", false, "")); got == nil {
		t.Fatal("tool_call must emit")
	}
	act := feed.OnEvent("stream", streamEvent(
		"assistant", "r-1", "c-1", "tool_result", "", true, ""))
	if act == nil || act.Phase != PetPhaseError {
		t.Fatalf("error activity = %+v", act)
	}
	if act.Tool == nil || act.Tool.Name != "exec_command" ||
		act.Tool.Category != PetToolCategoryExec {
		t.Fatalf("error retained tool = %+v", act.Tool)
	}
}

func TestFeedTurnEndFailure(t *testing.T) {
	feed := NewPetActivityFeed("assistant")
	act := feed.OnEvent("turn_end", turnEndEvent(
		"assistant", "r-1", "c-1", "failed", "boom"))
	if act == nil || act.Phase != PetPhaseError {
		t.Fatalf("failed turn activity = %+v", act)
	}
}

func TestFeedAskingResolved(t *testing.T) {
	feed := NewPetActivityFeed("assistant")
	act := feed.OnEvent("interact", interactEvent("r-1", "c-1"))
	if act == nil || act.Phase != PetPhaseAsking {
		t.Fatalf("asking activity = %+v", act)
	}
	// Another run answers in parallel; asking must still win.
	if got := feed.OnEvent("stream", streamEvent(
		"assistant", "r-2", "c-2", "text", "", false, "parallel")); got != nil {
		t.Fatalf("asking must keep the visible state, got %+v", got)
	}
	if snap := feed.Snapshot("assistant"); snap == nil ||
		snap.Phase != PetPhaseAsking {
		t.Fatalf("asking must outrank answering, snapshot = %+v", snap)
	}
	if got := feed.OnEvent("resolved", map[string]any{
		"id":              "prompt-1",
		"conversation_id": "c-1",
	}); got == nil || got.Phase != PetPhaseAnswering {
		t.Fatalf("after resolve visible activity = %+v", got)
	}
	if snap := feed.Snapshot("assistant"); snap == nil ||
		snap.ConversationID != "c-2" {
		t.Fatalf("snapshot after resolve = %+v", snap)
	}
}

func TestFeedMultiRunAggregation(t *testing.T) {
	feed := NewPetActivityFeed("assistant")
	if got := feed.OnEvent("stream", streamEvent(
		"assistant", "r-a", "c-a", "text", "", false, "first")); got == nil ||
		got.Phase != PetPhaseAnswering {
		t.Fatalf("run A answering = %+v", got)
	}
	act := feed.OnEvent("stream", streamEvent(
		"assistant", "r-b", "c-b", "tool_call", "write_file", false, ""))
	if act == nil || act.Phase != PetPhaseTool || act.RunID != "r-b" {
		t.Fatalf("run B tool must outrank run A answering: %+v", act)
	}
	act = feed.OnEvent("turn_end", turnEndEvent(
		"assistant", "r-b", "c-b", "completed", ""))
	if act == nil || act.Phase != PetPhaseAnswering || act.RunID != "r-a" {
		t.Fatalf("after B completes visible must fall back to A: %+v", act)
	}
}

func TestFeedIgnoresUnknownEvents(t *testing.T) {
	feed := NewPetActivityFeed("assistant")
	if got := feed.OnEvent("session_updated", map[string]any{"id": "c-1"}); got != nil {
		t.Fatalf("unknown event emitted %+v", got)
	}
	if got := feed.OnEvent("stream", map[string]any{}); got != nil {
		t.Fatalf("malformed stream emitted %+v", got)
	}
}

func TestPetDirectorTransitions(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	feed := NewPetActivityFeed("assistant")
	now := base
	feed.now = func() time.Time { return now }
	director := NewPetDirector(feed)

	if state := director.Poll("assistant", now); state.Disposition != PetDispositionRoam {
		t.Fatalf("no activity must roam, got %+v", state)
	}

	now = base
	if got := feed.OnEvent("stream", streamEvent(
		"assistant", "r-1", "c-1", "reasoning", "", false, "think")); got == nil {
		t.Fatal("reasoning event must emit")
	}
	state := director.Poll("assistant", now.Add(100*time.Millisecond))
	if state.Disposition != PetDispositionWork || state.Phase != PetPhaseThinking {
		t.Fatalf("recent reasoning must work/think, got %+v", state)
	}

	now = base.Add(200 * time.Millisecond)
	if got := feed.OnEvent("interact", interactEvent("r-1", "c-1")); got == nil {
		t.Fatal("interact event must emit")
	}
	state = director.Poll("assistant", now.Add(100*time.Millisecond))
	if state.Disposition != PetDispositionAsk || !state.Interactive {
		t.Fatalf("asking must be interactive, got %+v", state)
	}

	state = director.Poll("assistant", now.Add(5*time.Second))
	if state.Disposition != PetDispositionRoam {
		t.Fatalf("idle timeout must roam, got %+v", state)
	}
	state = director.Poll("assistant", now.Add(95*time.Second))
	if state.Disposition != PetDispositionSleep {
		t.Fatalf("sleep timeout must sleep, got %+v", state)
	}
}

func TestFeedPrunesStaleRuns(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	feed := NewPetActivityFeed("assistant")
	now := base
	feed.now = func() time.Time { return now }

	if got := feed.OnEvent("interact", interactEvent("r-old", "c-old")); got == nil {
		t.Fatal("old asking run must register")
	}
	now = base.Add(11 * time.Minute)
	act := feed.OnEvent("stream", streamEvent(
		"assistant", "r-new", "c-new", "text", "", false, "fresh"))
	if act == nil || act.RunID != "r-new" || act.Phase != PetPhaseAnswering {
		t.Fatalf("stale asking must not shadow fresh answering, got %+v", act)
	}
}

func TestFeedActivitiesPerAgent(t *testing.T) {
	feed := NewPetActivityFeed("assistant")
	if got := feed.OnEvent("stream", streamEvent(
		"assistant", "r-1", "c-1", "text", "", false, "main")); got == nil {
		t.Fatal("assistant event must register")
	}
	if got := feed.OnEvent("stream", streamEvent(
		"research-agent", "r-2", "c-2", "tool_call", "web_fetch", false, "")); got == nil {
		t.Fatal("subagent event must register")
	}
	activities := feed.Activities()
	if len(activities) != 2 {
		t.Fatalf("Activities length = %d, want 2: %+v", len(activities), activities)
	}
	var research *PetActivity
	for i := range activities {
		if activities[i].AgentID == "research-agent" {
			research = &activities[i]
		}
	}
	if research == nil || research.Phase != PetPhaseTool {
		t.Fatalf("subagent activity = %+v", research)
	}
}
