package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestSecondTurnReplaysStructuredToolPair drives two turns against the
// real assistant graph: the first performs a tool call (write_file),
// the second continues the same conversation. It then inspects the
// provider request of the second turn and asserts the persisted tool
// result is replayed as a real role=tool message paired with its
// assistant tool_call — not degraded to user text.
func TestSecondTurnReplaysStructuredToolPair(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "write_file",
			Arguments: `{"file_path":"out.txt","content":"hello\n"}`,
		}}},
		fakeprovider.Reply{Text: "first turn done"},
		fakeprovider.Reply{Text: "second turn ok"},
	)
	workDir := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeInferenceConfig(t, configDir, provider.URL())

	ctx := context.Background()
	mgr := host.NewManagerAt(filepath.Dir(configDir), configDir)
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() {
		_ = h.Close()
	}()

	first, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "write out.txt"),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if res, err := first.Wait(ctx); err != nil || res.Status != "completed" {
		t.Fatalf("first run = %+v, %v; want completed", res, err)
	}
	if provider.Calls() != 2 {
		t.Fatalf("provider calls after first turn = %d, want 2", provider.Calls())
	}

	second, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "continue"),
		ContextID:     first.ContextID(),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}
	if res, err := second.Wait(ctx); err != nil || res.Status != "completed" {
		t.Fatalf("second run = %+v, %v; want completed", res, err)
	}
	if provider.Calls() != 3 {
		t.Fatalf("provider calls after second turn = %d, want 3", provider.Calls())
	}

	msgs, err := provider.LastMessages()
	if err != nil {
		t.Fatalf("LastMessages: %v", err)
	}
	var callID, resultID string
	callCount, toolCount := 0, 0
	callIdx, resultIdx := -1, -1
	for i, m := range msgs {
		role, _ := m["role"].(string)
		if role == "assistant" {
			if ids := wireCallIDs(t, m); len(ids) > 0 {
				callID = ids[0]
				callCount++
				callIdx = i
			}
		}
		if role == "tool" {
			if id, ok := m["tool_call_id"].(string); ok && id != "" {
				resultID = id
				toolCount++
				resultIdx = i
			}
		}
	}
	if callID == "" || resultID == "" || callID != resultID ||
		callCount != 1 || toolCount != 1 || callIdx >= resultIdx {
		var dump string
		for _, m := range msgs {
			role, _ := m["role"].(string)
			raw, _ := json.Marshal(m)
			dump += "\n[" + role + "] " + string(raw)
		}
		t.Fatalf("second-turn request must pair tool_call %q with tool_call_id %q "+
			"(calls=%d results=%d callIdx=%d resultIdx=%d)\n%s",
			callID, resultID, callCount, toolCount, callIdx, resultIdx, dump)
	}

	// Full second-round channel shape: the base system prefix comes
	// first and nothing system-role follows the first user-role content;
	// the replayed first-turn request sits before the current turn, and
	// the current user turn is last.
	if first, ok := msgs[0]["role"].(string); !ok || first != "system" {
		t.Fatalf("first message role = %#v, want system", msgs[0]["role"])
	}
	if !strings.Contains(messageText(t, msgs[0]), "You are opencraft") {
		t.Fatalf("first system message must carry the base identity, got %q",
			messageText(t, msgs[0]))
	}
	last := msgs[len(msgs)-1]
	if role, _ := last["role"].(string); role != "user" ||
		!strings.Contains(messageText(t, last), "continue") {
		t.Fatalf("last message = %+v, want the current user turn", last)
	}
	sawUser := false
	for i, m := range msgs {
		role, _ := m["role"].(string)
		if role == "user" {
			sawUser = true
		}
		if role == "system" && sawUser {
			t.Fatalf("system message follows user content at index %d: %q",
				i, messageText(t, m))
		}
	}
	firstTurnIdx, currentTurnIdx := -1, -1
	for i, m := range msgs {
		role, _ := m["role"].(string)
		if role != "user" {
			continue
		}
		switch text := messageText(t, m); {
		case strings.Contains(text, "write out.txt"):
			firstTurnIdx = i
		case strings.Contains(text, "continue"):
			currentTurnIdx = i
		}
	}
	if firstTurnIdx < 0 || currentTurnIdx < 0 || firstTurnIdx >= currentTurnIdx {
		t.Fatalf("replayed first turn (idx %d) must precede current turn (idx %d)",
			firstTurnIdx, currentTurnIdx)
	}
}

// wireCallIDs extracts tool_call ids from an OpenAI-style assistant
// message.
func wireCallIDs(t *testing.T, m map[string]any) []string {
	t.Helper()
	raw, ok := m["tool_calls"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range raw {
		call, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := call["id"].(string); ok && id != "" {
			out = append(out, id)
		}
	}
	return out
}
