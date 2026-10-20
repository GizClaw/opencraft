package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// benchmarkCheckpoint is the wave checkpoint of a replay turn: the world
// sections, a replayed conversation of the size a long session reaches,
// and this turn's messages. The trim exists so this shape does not get
// re-serialized per wave; the benchmark is the guard for it.
func benchmarkCheckpoint(historyMessages int) agent.Checkpoint {
	board := agent.NewBoard()
	board.SetVar("world.sections.count", 4)
	board.SetVar("world.history.count", historyMessages)
	board.SetVar(boardVarThreadID, "oc-s-bench")
	for range 4 {
		board.AppendChannelMessage(agent.MainChannel,
			message.NewTextMessage(message.RoleSystem, strings.Repeat("section ", 1200)))
	}
	for i := range historyMessages {
		role := message.RoleUser
		if i%2 == 1 {
			role = message.RoleAssistant
		}
		board.AppendChannelMessage(agent.MainChannel,
			message.NewTextMessage(role, fmt.Sprintf("replayed %d %s", i, strings.Repeat("x", 1400))))
	}
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleUser, "this turn's ask"))
	board.AppendChannelMessage(agent.MainChannel,
		message.NewTextMessage(message.RoleAssistant, "this turn's answer"))
	request := agent.Request{
		ContextID: "s-bench",
		Message:   message.NewTextMessage(message.RoleUser, "this turn's ask"),
	}
	raw, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	return agent.Checkpoint{
		ExecID:    "run-bench00000001",
		Steps:     []string{"world"},
		Iteration: 1,
		Board:     board.Snapshot(),
		Attributes: map[string]string{
			checkpointAttrConversationID: "s-bench",
			checkpointAttrRequest:        string(raw),
		},
	}
}

// BenchmarkCheckpointSave measures one wave boundary end to end: the
// trim the host applies, then the encode the store performs. A run
// stamps one of these per completed wave, so this number is paid
// hundreds of times per long turn.
func BenchmarkCheckpointSave(b *testing.B) {
	cp := benchmarkCheckpoint(2000)
	full, err := json.Marshal(cp)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("full checkpoint = %.1f MB", float64(len(full))/(1<<20))

	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		trimmed := trimCheckpoint(ctx, cp)
		raw, err := json.Marshal(trimmed)
		if err != nil {
			b.Fatal(err)
		}
		if len(raw) > 1<<20 {
			b.Fatalf("trimmed checkpoint = %d bytes, want the turn only", len(raw))
		}
	}
}

// BenchmarkCheckpointSaveUntrimmed is the same encode without the trim,
// for comparison: it is the cost every wave paid before the trim landed.
func BenchmarkCheckpointSaveUntrimmed(b *testing.B) {
	cp := benchmarkCheckpoint(2000)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(cp); err != nil {
			b.Fatal(err)
		}
	}
}
