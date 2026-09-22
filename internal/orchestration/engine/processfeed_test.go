package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// TestExecSessionReachesTheProcessFeed pins the wiring behind the
// activity card's process section: the deployed sandbox runner taps
// every session a conversation starts, and the assembled exec_session
// tool goes through that runner exactly like the one-shot
// exec_command. The tap is what lets the card show a running server —
// and its output — without the model having to read it, so a session
// that never registers is invisible no matter how long it runs.
//
// The test drives the tools the way the tool node does: through the
// assembled dispatcher, with the RunInfo-bearing context flowcraft
// injects during graph execution.
func TestExecSessionReachesTheProcessFeed(t *testing.T) {
	rt, store := buildEmbeddedRuntime(t)
	feed := runtimeFeed(t, rt)
	tools := runtimeTools(t, rt)

	conv, err := store.Create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// YOLO keeps the run away from the approval gate: this test is
	// about the tap, not about who may run what (the confined chain
	// taps through the same Start).
	if err := store.SetMode(
		context.Background(), conv, sessions.ModeYOLO,
	); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{
		Identity: agent.Identity{AgentID: "assistant", ConversationID: conv},
	})

	executeTool(t, tools, ctx, "exec_session", map[string]any{
		"action":     "start",
		"process_id": "dev",
		"argv":       []string{"/bin/sh", "-c", "echo ready; sleep 30"},
	})

	// The row must be there while the process still runs, with the
	// output the model never read.
	entry := awaitProcess(t, feed, conv, func(p sandbox.Process) bool {
		return p.Running && p.ID == "dev" && strings.Contains(p.Tail, "ready")
	})
	if got := strings.Join(entry.Argv, " "); got != "/bin/sh -c echo ready; sleep 30" {
		t.Errorf("argv = %q", got)
	}
	if entry.TTY {
		t.Error("session reported as a tty session")
	}
	if entry.ExitCode != nil || entry.ExitReason != "" {
		t.Errorf("running session carries an exit status: %+v", entry)
	}

	// Closing the session is what stops the row: the card reports live
	// work, and the feed marks the entry stopped as soon as the handle
	// closes.
	executeTool(t, tools, ctx, "exec_session", map[string]any{
		"action":     "close",
		"process_id": "dev",
	})
	awaitProcess(t, feed, conv, func(p sandbox.Process) bool {
		return p.ID == "dev" && !p.Running
	})

	// A one-shot command lands in the same feed, tail included: the
	// tool closes its session as soon as the command ends, and the
	// close-time salvage is what keeps a short command's output from
	// racing the drain.
	executeTool(t, tools, ctx, "exec_command", map[string]any{
		"command": "echo one-shot",
	})
	awaitProcess(t, feed, conv, func(p sandbox.Process) bool {
		return !p.Running && strings.Contains(p.Tail, "one-shot")
	})
}

func runtimeFeed(t *testing.T, rt *runtimecore.Runtime) *sandbox.ProcessFeed {
	t.Helper()
	value, ok := rt.Resource("processes")
	if !ok {
		t.Fatal("processes resource missing")
	}
	feed, ok := value.(*sandbox.ProcessFeed)
	if !ok || feed == nil {
		t.Fatalf("processes resource is %T", value)
	}
	return feed
}

func runtimeTools(t *testing.T, rt *runtimecore.Runtime) *tool.Assembly {
	t.Helper()
	value, ok := rt.Resource("tools")
	if !ok {
		t.Fatal("tools resource missing")
	}
	asm, ok := value.(*tool.Assembly)
	if !ok || asm == nil {
		t.Fatalf("tools resource is %T", value)
	}
	return asm
}

// executeTool dispatches one call and fails the test when the tool
// reported an error.
func executeTool(
	t *testing.T,
	asm *tool.Assembly,
	ctx context.Context,
	name string,
	args map[string]any,
) message.ToolResult {
	t.Helper()
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal %s args: %v", name, err)
	}
	res := asm.Execute(ctx, message.ToolCall{
		ID:        "call-1",
		Name:      name,
		Arguments: payload,
	})
	if res.IsError {
		t.Fatalf("%s: %s", name, res.Content)
	}
	return res
}

// awaitProcess polls the conversation's feed until one entry matches,
// and reports what the feed held when it did not.
func awaitProcess(
	t *testing.T,
	feed *sandbox.ProcessFeed,
	conversationID string,
	match func(sandbox.Process) bool,
) sandbox.Process {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		procs := feed.List(conversationID)
		for _, p := range procs {
			if match(p) {
				return p
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("feed never matched; entries: %+v", procs)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
