package worldstate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/plan"
)

// The world node is the one graph script that prepends model-visible
// context (instructions, environment, permissions, memory) to every
// turn. Go writes the payload into board vars and the node replays it
// onto the MainChannel, so two things have to agree and nothing else
// checks them:
//
//   - the board var names on both sides, and
//   - the JSON shape of a section (id/role/content).
//
// A mismatch is silent and severe: the model loses its instructions or
// the count markers degrade, and the memory hooks that locate the user
// turn on the channel start slicing in the wrong place. The fixture
// below carries what Go actually writes, and
// frontend/src/lib/worldNodeMirror.test.ts runs world.js against it.

type worldNodeCase struct {
	Name string `json:"name"`
	// Sections is the string Go writes to world.sections.
	Sections string `json:"sections"`
	// History is the string Go writes to world.history; empty when the
	// turn does not replay history, in which case the var is unset.
	History string `json:"history,omitempty"`
	// TailBlock is the string Go writes to world.tail_block; empty when
	// the turn has no per-turn context, in which case the var is unset.
	TailBlock string `json:"tail_block,omitempty"`
	// Existing is the channel the node finds on the board when it runs.
	Existing []json.RawMessage `json:"existing"`
}

type worldNodeFixture struct {
	Note  string          `json:"note"`
	Cases []worldNodeCase `json:"cases"`
}

// worldNodeFixtureCases renders real world state for the cases the
// node has to survive: an ordinary turn, a full-history replay turn
// where world.history is set as well, and a turn carrying a per-turn
// tail block (plan + skills) that the node has to append to the turn's
// own message.
func worldNodeFixtureCases(t *testing.T, workBase string) []worldNodeCase {
	t.Helper()
	// Skill discovery always scans the user's own ~/.agents/skills, and the
	// skills section embeds every discovered SKILL.md path. Without
	// isolating HOME this fixture would bake the developer's home paths and
	// skill names into a checked-in golden file, and every machine with a
	// different HOME would fail the comparison. userDir is pointed at a
	// temp dir for the same reason (its skills root would otherwise be
	// scanned in addition to the home one).
	userDir := isolateUserHome(t)
	existing := []message.Message{
		message.NewTextMessage(message.RoleUser, "the user's current turn"),
	}
	// normalize keeps the fixture stable across runs and machines: the
	// environment section embeds the workspace root, and an activated skill
	// embeds the user data dir (the staged copy it points at). Both live in
	// per-test temp dirs.
	replacer := strings.NewReplacer(
		workBase, "<workbase>",
		userDir, "<userdir>",
	)
	normalize := func(s string) string { return replacer.Replace(s) }
	build := func(name, prompt string, svc *Service, ch []message.Message) worldNodeCase {
		t.Helper()
		board := agent.NewBoard()
		if err := svc.RenderToBoard(
			context.Background(), "assistant", "s-c1", prompt, nil, board,
		); err != nil {
			t.Fatalf("%s: render: %v", name, err)
		}
		entry := worldNodeCase{
			Name:      name,
			Sections:  normalize(board.GetVarString("world.sections")),
			History:   normalize(board.GetVarString("world.history")),
			TailBlock: normalize(board.GetVarString("world.tail_block")),
			// Always an array, never null: the mirror test iterates it.
			Existing: []json.RawMessage{},
		}
		for _, m := range ch {
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("%s: marshal existing: %v", name, err)
			}
			entry.Existing = append(entry.Existing, raw)
		}
		return entry
	}
	plain := New(Options{
		WorkBase:          workBase,
		UserDir:           userDir,
		CollaborationMode: "workspace",
	})
	replay := New(Options{
		WorkBase:          workBase,
		UserDir:           userDir,
		CollaborationMode: "workspace",
	})
	replay.memory = replayMemory{stubMemory{items: []corememory.ContextItem{
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleUser,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "replayed user turn"},
			}},
		},
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "replayed assistant turn"},
			}},
		},
	}}}

	// Tail case: a live plan plus a $mentioned skill, i.e. exactly the
	// content that must ride with the user's message instead of becoming
	// messages of its own.
	writeSkillFile(t, workBase, "review", "review code and docs")
	tail := New(Options{
		WorkBase:          workBase,
		UserDir:           userDir,
		CollaborationMode: "workspace",
	})
	tail.SetSkills(skills.NewService(context.Background(), skills.Options{
		WorkBase: workBase, UserDir: userDir, Enabled: true, TopN: 5,
	}))
	sess := newSessionStore(t)
	if _, err := plan.NewStore(sess).Update("assistant", "s-c1",
		plan.UpdatePlanArgs{Plan: []plan.PlanItem{
			{Step: "inspect the fixture", Status: plan.StatusInProgress},
		}}); err != nil {
		t.Fatalf("tail case plan: %v", err)
	}
	tail.SetSessions(sess)
	return []worldNodeCase{
		build("plain turn", "hi", plain, existing),
		build("full-history replay", "hi", replay, existing),
		build("per-turn tail (plan + skills)", "use $review", tail, existing),
		// A board the node finds with no turn message at all (a custom
		// engine that seeds its own channel): the block must still reach
		// the model, as its own message.
		build("tail without a turn message", "use $review", tail, nil),
	}
}

// isolateUserHome points HOME (and USERPROFILE, so the Windows path of
// os.UserHomeDir behaves the same way) at a temp dir and returns a temp
// user data dir. Anything that scans user-level skill roots must call it
// before the first discovery, or the golden fixture captures whatever is
// installed on the machine running the test.
func isolateUserHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return t.TempDir()
}

// TestWorldNodeFixtureMatchesRenderedState pins the fixture to what Go
// writes. Run with UPDATE_GOLDEN=1 after an intentional change, then run
// the frontend test that reads the same file.
func TestWorldNodeFixtureMatchesRenderedState(t *testing.T) {
	workBase := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workBase, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workBase, "AGENTS.md"),
		[]byte("project rules for the fixture"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	fixture := worldNodeFixture{
		Note: "Generated by TestWorldNodeFixtureMatchesRenderedState " +
			"(UPDATE_GOLDEN=1). The graph's world node is checked against " +
			"this file by frontend/src/lib/worldNodeMirror.test.ts.",
		Cases: worldNodeFixtureCases(t, workBase),
	}
	got, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "world_cases.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(got))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("fixture is stale; rerun with UPDATE_GOLDEN=1:\n--- want\n%s\n--- got\n%s",
			want, got)
	}
}
