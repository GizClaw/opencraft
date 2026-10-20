package worldstate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/agent/bindings"
	corememory "github.com/GizClaw/flowcraft/core/memory"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/plan"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// The world node is the one graph node that seeds model-visible context
// (instructions, environment, permissions, memory) in front of every
// turn. Go renders the payload into board vars and Merge replays it
// onto the MainChannel, so three things have to hold at once:
//
//   - the sections come first, then the replayed history, then the
//     turn's own messages: the memory hooks slice the persisted
//     transcript by the two counts, and the model reads the prefix in
//     that order;
//   - the two count vars say how many messages Merge prepended, because
//     that is how the hooks find the user's turn message;
//   - the per-turn tail block (plan, skills) rides the turn's own user
//     message, and becomes a message of its own only when there is no
//     turn message to ride.
//
// This used to be a checked-in fixture that a frontend test ran world.js
// against. Both halves are Go now, so the same cases are asserted
// directly — including the one only the JS could reach before (a tail
// block with no user message on the channel to carry it).

func TestMergeSeedsTheChannelFromRenderedWorldState(t *testing.T) {
	workBase := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(workBase, "AGENTS.md"),
		[]byte("project rules for the merge test"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	userDir := isolateUserHome(t)

	// An ordinary turn: the prepare hook wrote sections and nothing else.
	plain := New(Options{
		WorkBase:          workBase,
		UserDir:           userDir,
		CollaborationMode: "workspace",
	})
	// Full-history replay: world.history is set as well, and both counts
	// have to be right for the hooks to find the turn message.
	replay := New(Options{
		WorkBase:          workBase,
		UserDir:           userDir,
		CollaborationMode: "workspace",
	})
	replay.memory = replayMemory{stubMemory{items: []corememory.ContextItem{
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleUser,
			Content:     message.NewTextContent("replayed user turn"),
		},
		{
			Kind:        corememory.ContextRawMessage,
			MessageRole: message.RoleAssistant,
			Content:     message.NewTextContent("replayed assistant turn"),
		},
	}}}
	// A live plan plus a $mentioned skill: exactly the content that must
	// ride the user's message instead of becoming messages of its own.
	writeSkillFile(t, workBase, "review", "review code and docs")
	tail := New(Options{
		WorkBase:          workBase,
		UserDir:           userDir,
		CollaborationMode: "workspace",
	})
	tail.SetSkills(skills.NewService(context.Background(), skills.Options{
		UserDir: userDir, Enabled: true, TopN: 5,
	}))
	sess := newSessionStore(t)
	if _, err := plan.NewStore(sess).Update("assistant", "s-c1",
		plan.UpdatePlanArgs{Plan: []plan.PlanItem{
			{Step: "inspect the merge", Status: plan.StatusInProgress},
		}}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	tail.SetSessions(sess)

	turn := message.NewTextMessage(message.RoleUser, "the user's current turn")
	for _, tc := range []struct {
		name string
		svc  *Service
		// channel is what the call-site history preparer left on the
		// board: the restored conversation plus this turn's message.
		channel []message.Message
		// ridesLast says the tail block must ride the last message of the
		// channel; ownMessage says it must be emitted after it.
		ridesLast  bool
		ownMessage bool
		// historyLen is how many replayed messages the turn renders; the
		// replay mode is the only one that sets world.history at all.
		historyLen int
	}{
		{name: "plain turn", svc: plain, channel: []message.Message{turn}},
		{
			name: "full-history replay", svc: replay,
			channel: []message.Message{turn}, historyLen: 2,
		},
		{
			name: "per-turn tail (plan + skills)", svc: tail,
			channel: []message.Message{turn}, ridesLast: true,
		},
		{
			// A board with no turn message at all (a custom engine
			// seeding its own channel): the block must still reach the
			// model, as a user-role message of its own.
			name: "tail without a turn message", svc: tail,
			channel: nil, ownMessage: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			board := mergeBoard(t, tc.svc, "use $review", tc.channel)
			sections := boardSections(t, board)
			history := boardHistory(t, board)
			tailBlock := board.GetVarString(config.BoardVarTailBlock)
			if len(sections) == 0 {
				t.Fatal("the turn rendered no world sections")
			}
			if len(history) != tc.historyLen {
				t.Fatalf("%s carries %d replayed messages, want %d",
					BoardVarHistory, len(history), tc.historyLen)
			}
			if (tc.ridesLast || tc.ownMessage) && tailBlock == "" {
				t.Fatal("the tail case rendered no world.tail_block")
			}

			// The counts the memory hooks read to tell injected context
			// from the user's own turn.
			if got := countVar(t, board, BoardVarSectionsCount); got != len(sections) {
				t.Fatalf("%s = %d, want %d",
					BoardVarSectionsCount, got, len(sections))
			}
			if got := countVar(t, board, BoardVarHistoryCount); got != len(history) {
				t.Fatalf("%s = %d, want %d",
					BoardVarHistoryCount, got, len(history))
			}

			// Sections first, then replayed history, then whatever the
			// channel already held, with the tail block appended to the
			// turn's own message.
			want := make([]message.Message, 0,
				len(sections)+len(history)+len(tc.channel)+1)
			want = append(want, boardMessages(sections)...)
			want = append(want, history...)
			want = append(want, tc.channel...)
			switch {
			case tc.ridesLast:
				last := len(want) - 1
				want[last] = withTailPart(want[last], tailBlock)
			case tc.ownMessage:
				want = append(want, message.NewTextMessage(
					message.RoleUser, tailBlock))
			}

			got := board.Channel(agent.MainChannel)
			if len(got) != len(want) {
				t.Fatalf("channel has %d messages, want %d (sections %d, history %d, channel %d)",
					len(got), len(want), len(sections), len(history), len(tc.channel))
			}
			for i := range want {
				if !sameMessage(t, got[i], want[i]) {
					t.Fatalf("channel[%d] = %s, want %s",
						i, jsonOf(t, got[i]), jsonOf(t, want[i]))
				}
			}

			// Injected context is never attributed to an assistant or a
			// tool message: the model must read it as context behind the
			// user's ask.
			if tailBlock != "" {
				carrier := got[len(got)-1]
				if carrier.Role != message.RoleUser {
					t.Fatalf("tail rides a %q message, want user", carrier.Role)
				}
				if !strings.HasSuffix(carrier.Content.Text(), tailBlock) {
					t.Fatalf("tail carrier text does not end with the block: %q",
						carrier.Content.Text())
				}
			}

			// Every seeded message must be one the engine accepts: a
			// role plus at least one content part.
			for i, msg := range got {
				if msg.Role == "" || len(msg.Content.Parts) == 0 {
					t.Fatalf("channel[%d] = %s, want a role and at least one part",
						i, jsonOf(t, msg))
				}
			}
		})
	}
}

// TestMergeLeavesTheTurnMessageAloneWithoutATail keeps the other side of
// the tail rule: no block, no appended part. A stale world.tail_block on
// a reused board would otherwise ride the user's message as if it were
// current context.
func TestMergeLeavesTheTurnMessageAloneWithoutATail(t *testing.T) {
	svc := New(Options{WorkBase: t.TempDir(), UserDir: isolateUserHome(t)})
	turn := message.NewTextMessage(message.RoleUser, "hi")
	board := mergeBoard(t, svc, "hi", []message.Message{turn})
	if tail := board.GetVarString(config.BoardVarTailBlock); tail != "" {
		t.Fatalf("render wrote a tail block for a turn with no per-turn context: %q", tail)
	}
	got := board.Channel(agent.MainChannel)
	last := got[len(got)-1]
	if !sameMessage(t, last, turn) || len(last.Content.Parts) != 1 {
		t.Fatalf("turn message = %s, want the user's own text untouched",
			jsonOf(t, last))
	}
}

// TestMergeReportsAMalformedVar makes a broken payload loud: the node
// used to fail the run through JSON.parse, and silently seeding a turn
// without its instructions would be worse than stopping.
func TestMergeReportsAMalformedVar(t *testing.T) {
	board := agent.NewBoard()
	board.SetVar(BoardVarSections, "{not json")
	if _, err := Merge(board); err == nil {
		t.Fatal("Merge accepted a malformed world.sections")
	}
}

// BenchmarkMergeAgainstScriptBridge moves one turn's transcript both
// ways: Merge, and the read-the-channel / write-it-back pair the world
// node performed through the board bridge before it. The bridge arm is
// the JSON projection per message the script path paid in each
// direction (goja's own conversion of both sides sat on top of it); the
// merge arm is what a turn pays now.
//
// world.sections is left unset in both arms on purpose: the sections
// payload crosses a JSON string before and after this change, so it
// would be a constant on both sides rather than the thing being
// compared.
func BenchmarkMergeAgainstScriptBridge(b *testing.B) {
	for _, size := range []int{50, 200, 1000, 4000} {
		transcript := make([]message.Message, 0, size)
		for i := 0; i < size; i++ {
			transcript = append(transcript, message.NewTextMessage(
				message.RoleAssistant, strings.Repeat("turn text ", 100)))
		}
		// A fresh board each iteration, outside the timer: Merge prepends
		// the sections it finds, so reusing one board would grow the
		// channel under the measurement.
		b.Run(fmt.Sprintf("%d messages/go merge", size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				board := agent.NewBoard()
				board.SetChannel(agent.MainChannel, transcript)
				b.StartTimer()
				if _, err := Merge(board); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("%d messages/script bridge", size), func(b *testing.B) {
			board := agent.NewBoard()
			board.SetChannel(agent.MainChannel, transcript)
			_, value := bindings.NewBoardBridge(board)(context.Background())
			bridge := value.(map[string]any)
			read := bridge["channel"].(func(string) ([]any, error))
			write := bridge["setChannel"].(func(string, any) error)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				msgs, err := read(agent.MainChannel)
				if err != nil {
					b.Fatal(err)
				}
				if err := write(agent.MainChannel, msgs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// isolateUserHome points HOME (and USERPROFILE, so the Windows path of
// os.UserHomeDir behaves the same way) at a temp dir and returns a temp
// user data dir. Anything that scans user-level skill roots must call it
// before the first discovery, or the render picks up whatever is
// installed on the machine running the test.
func isolateUserHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return t.TempDir()
}

func mergeBoard(
	t *testing.T, svc *Service, prompt string, channel []message.Message,
) *agent.Board {
	t.Helper()
	board := agent.NewBoard()
	if err := svc.RenderToBoard(
		context.Background(), "assistant", "s-c1", prompt, nil, board,
	); err != nil {
		t.Fatalf("render: %v", err)
	}
	board.SetChannel(agent.MainChannel, channel)
	if _, err := Merge(board); err != nil {
		t.Fatalf("merge: %v", err)
	}
	return board
}

func boardSections(t *testing.T, board *agent.Board) []Section {
	t.Helper()
	var sections []Section
	if raw := board.GetVarString(BoardVarSections); raw != "" {
		if err := json.Unmarshal([]byte(raw), &sections); err != nil {
			t.Fatalf("decode %s: %v", BoardVarSections, err)
		}
	}
	return sections
}

func boardHistory(t *testing.T, board *agent.Board) []message.Message {
	t.Helper()
	var history []message.Message
	if raw := board.GetVarString(BoardVarHistory); raw != "" {
		if err := json.Unmarshal([]byte(raw), &history); err != nil {
			t.Fatalf("decode %s: %v", BoardVarHistory, err)
		}
	}
	return history
}

// boardMessages is what the merge puts on the channel for a section: its
// role and content, without the section id.
func boardMessages(sections []Section) []message.Message {
	out := make([]message.Message, 0, len(sections))
	for _, section := range sections {
		out = append(out, message.Message{
			Role:    section.Role,
			Content: section.Content,
		})
	}
	return out
}

func countVar(t *testing.T, board *agent.Board, name string) int {
	t.Helper()
	value, ok := board.GetVar(name)
	if !ok {
		t.Fatalf("%s is unset", name)
	}
	n, ok := value.(int)
	if !ok {
		t.Fatalf("%s = %T, want int", name, value)
	}
	return n
}

// withTailPart returns msg with the tail block appended as a final text
// part, which is how the merge delivers it to the turn's own message.
func withTailPart(msg message.Message, tail string) message.Message {
	parts := make([]message.Part, 0, len(msg.Content.Parts)+1)
	parts = append(parts, msg.Content.Parts...)
	parts = append(parts, message.TextPart{Text: tail})
	msg.Content = message.Content{Parts: parts}
	return msg
}

// sameMessage compares messages by their wire form: that is what the
// model receives, and it is insensitive to which Go part value the
// clone normalized to.
func sameMessage(t *testing.T, a, b message.Message) bool {
	t.Helper()
	return bytes.Equal(jsonOf(t, a), jsonOf(t, b))
}

func jsonOf(t *testing.T, msg message.Message) []byte {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	return raw
}
