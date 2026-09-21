package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// steerGateTimeout bounds how long a test waits for the fake provider to
// receive the gated request. The gate itself is what keeps the turn
// deterministic: the first inference round cannot finish until the test
// releases it, so a steer submitted while it is held can only be
// delivered by the boundary between rounds.
const steerGateTimeout = 30 * time.Second

// steerHost starts one Host on a fresh workspace, wired to the fake
// provider.
func steerHost(t *testing.T, provider *fakeprovider.Server) (*host.Host, context.Context) {
	t.Helper()
	workDir := t.TempDir()
	dataDir := t.TempDir()
	// The skills scan reads $HOME during assembly; keep the developer's
	// own ~/.agents/skills out of the world state.
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	ctx := context.Background()
	mgr := host.NewManagerAt(dataDir, configDir)
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, ctx
}

func waitSteerGate(t *testing.T, gate *fakeprovider.Gate) {
	t.Helper()
	select {
	case <-gate.Ready():
	case <-time.After(steerGateTimeout):
		t.Fatal("the first inference call never reached the provider")
	}
}

// steerText returns the plain text of one provider request message. Chat
// requests carry user content as a string; the array shape is tolerated
// so a driver change cannot silently produce empty assertions.
func steerText(t *testing.T, m map[string]any) string {
	t.Helper()
	switch c := m["content"].(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, p := range c {
			if part, ok := p.(map[string]any); ok {
				if text, ok := part["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		t.Fatalf("message content has unexpected shape %T: %#v", m["content"], m)
		return ""
	}
}

// steerCallID extracts the first tool_call id of an OpenAI-style
// assistant message.
func steerCallID(t *testing.T, m map[string]any) string {
	t.Helper()
	calls, _ := m["tool_calls"].([]any)
	for _, item := range calls {
		call, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := call["id"].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

// TestSteerDeliveredAtToolBoundary pins the steering contract on the
// real assistant graph: a message submitted while the first inference
// round is in flight is delivered at the round boundary the steer node
// owns — after the tool result, as a standalone user message, ahead of
// the next round's request.
func TestSteerDeliveredAtToolBoundary(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "write_file",
			Arguments: `{"file_path":"steer.txt","content":"steer\n"}`,
		}}},
		fakeprovider.Reply{Text: "done"},
	)
	hold := provider.HoldNext()
	defer hold.Release()

	h, ctx := steerHost(t, provider)
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "write steer.txt"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitSteerGate(t, hold)

	const steerLine = "actually, put it in notes/steer.txt instead"
	if err := h.SteerRun(run.RunID(), "  "+steerLine+"  "); err != nil {
		t.Fatalf("steer run: %v", err)
	}
	hold.Release()

	res, err := run.Wait(ctx)
	if err != nil || res == nil || res.Status != "completed" {
		t.Fatalf("run = %+v, %v; want completed", res, err)
	}
	if calls := provider.Calls(); calls != 2 {
		t.Fatalf("provider calls = %d, want 2", calls)
	}
	msgs, err := provider.LastMessages()
	if err != nil {
		t.Fatalf("last messages: %v", err)
	}
	if len(msgs) < 3 {
		t.Fatalf("second request carries %d messages, want the turn plus the steer line",
			len(msgs))
	}
	// The steered text closes the second request: a user message right
	// after the tool result that answered the assistant's tool call. The
	// text is stored trimmed, without the padding the caller submitted.
	last := msgs[len(msgs)-1]
	if role, _ := last["role"].(string); role != "user" {
		t.Fatalf("last message role = %v, want user", last["role"])
	}
	if text := steerText(t, last); text != steerLine {
		t.Fatalf("last message text = %q, want the steer line %q", text, steerLine)
	}
	result := msgs[len(msgs)-2]
	if role, _ := result["role"].(string); role != "tool" {
		t.Fatalf("message before the steer line role = %v, want tool", result["role"])
	}
	call := msgs[len(msgs)-3]
	if role, _ := call["role"].(string); role != "assistant" {
		t.Fatalf("message before the tool result role = %v, want assistant", call["role"])
	}
	id, _ := result["tool_call_id"].(string)
	if id == "" || id != steerCallID(t, call) {
		t.Fatalf("tool result %q does not answer the assistant call %q",
			id, steerCallID(t, call))
	}
}

// TestSteerRunRejections pins the host surface's refusal behavior: an
// unknown run, an empty message, and a full turn queue are all reported
// to the caller, and every message the turn did accept still reaches the
// model exactly once.
func TestSteerRunRejections(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "write_file",
			Arguments: `{"file_path":"steer.txt","content":"x\n"}`,
		}}},
		fakeprovider.Reply{Text: "done"},
	)
	hold := provider.HoldNext()
	defer hold.Release()

	h, ctx := steerHost(t, provider)
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "write steer.txt"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitSteerGate(t, hold)

	if err := h.SteerRun("run-missing", "hello"); err == nil {
		t.Fatal("steering an unknown run must fail")
	}
	if err := h.SteerRun(run.RunID(), "   "); err == nil {
		t.Fatal("steering an empty message must fail")
	}
	// The turn-owned queue is bounded: keep submitting until it refuses,
	// then assert the refusal is the queue-full classification rather
	// than something swallowed.
	accepted := 0
	var rejected error
	for i := 0; i < 64 && rejected == nil; i++ {
		err := h.SteerRun(run.RunID(), fmt.Sprintf("queued steer %d", i))
		if err != nil {
			rejected = err
			break
		}
		accepted++
	}
	if rejected == nil {
		t.Fatal("the turn accepted an unbounded number of steer messages")
	}
	if !errors.Is(rejected, coresession.ErrSteerQueueFull) {
		t.Fatalf("queue-full rejection = %v, want ErrSteerQueueFull", rejected)
	}
	if accepted == 0 {
		t.Fatal("no steer message was accepted before the queue filled")
	}
	hold.Release()

	res, err := run.Wait(ctx)
	if err != nil || res == nil || res.Status != "completed" {
		t.Fatalf("run = %+v, %v; want completed", res, err)
	}
	msgs, err := provider.LastMessages()
	if err != nil {
		t.Fatalf("last messages: %v", err)
	}
	delivered := 0
	for _, m := range msgs {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal message: %v", err)
		}
		delivered += strings.Count(string(raw), "queued steer")
	}
	if delivered != accepted {
		t.Fatalf("steer messages delivered = %d, want the %d accepted ones",
			delivered, accepted)
	}
}

// TestSteerRunTooLargeKeepsTheTurnAlive pins the payload refusal: a
// steer over core's 32 KiB budget is reported as ErrSteerTooLarge
// without touching the turn, and the next steer still lands. It also
// stands for the refusals the host surface cannot produce itself (a
// terminal turn's ErrSteerClosed, the barge-in window's
// agent.Interrupted): a rejected steer is refused, never half-queued.
func TestSteerRunTooLargeKeepsTheTurnAlive(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "write_file",
			Arguments: `{"file_path":"steer.txt","content":"x\n"}`,
		}}},
		fakeprovider.Reply{Text: "done"},
	)
	hold := provider.HoldNext()
	defer hold.Release()

	h, ctx := steerHost(t, provider)
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "write steer.txt"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	waitSteerGate(t, hold)

	oversized := strings.Repeat("oversized ", 4*1024)
	if err := h.SteerRun(run.RunID(), oversized); err == nil {
		t.Fatal("an oversized steer was accepted")
	} else if !errors.Is(err, coresession.ErrSteerTooLarge) {
		t.Fatalf("oversized steer = %v, want ErrSteerTooLarge", err)
	}
	// The refusal must not have poisoned the turn: the next message is
	// accepted and delivered like any other steer.
	if err := h.SteerRun(run.RunID(), "small correction"); err != nil {
		t.Fatalf("steer after the oversized refusal: %v", err)
	}
	hold.Release()

	res, err := run.Wait(ctx)
	if err != nil || res == nil || res.Status != "completed" {
		t.Fatalf("run = %+v, %v; want completed", res, err)
	}
	msgs, err := provider.LastMessages()
	if err != nil {
		t.Fatalf("last messages: %v", err)
	}
	raw, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	if strings.Contains(string(raw), "oversized") {
		t.Fatal("the refused steer text still reached the model")
	}
	if !strings.Contains(string(raw), "small correction") {
		t.Fatal("the steer submitted after the refusal never reached the model")
	}
}

// TestSteerRunAfterTheSettleIsRefused pins the other end: once the run
// settled and the host dropped it, a late steer is refused rather than
// queued behind a turn nobody drains. (Inside the live window the same
// refusal is core's ErrSteerClosed; the frontend classifies both as
// "the turn stopped accepting the message".)
func TestSteerRunAfterTheSettleIsRefused(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	h, ctx := steerHost(t, provider)
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "hello"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("wait run: %v", err)
	}
	calls := provider.Calls()

	if err := h.SteerRun(run.RunID(), "too late"); err == nil {
		t.Fatal("a steer for a settled run was accepted")
	}
	if got := provider.Calls(); got != calls {
		t.Fatalf("provider calls = %d after the late steer, want %d", got, calls)
	}
}
