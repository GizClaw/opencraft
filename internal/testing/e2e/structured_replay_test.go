package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	for _, m := range msgs {
		role, _ := m["role"].(string)
		if role == "assistant" {
			if ids := wireCallIDs(t, m); len(ids) > 0 {
				callID = ids[0]
			}
		}
		if role == "tool" {
			if id, ok := m["tool_call_id"].(string); ok && id != "" {
				resultID = id
			}
		}
	}
	if callID == "" || resultID == "" || callID != resultID {
		var dump string
		for _, m := range msgs {
			role, _ := m["role"].(string)
			raw, _ := json.Marshal(m)
			dump += "\n[" + role + "] " + string(raw)
		}
		t.Fatalf("second-turn request must pair tool_call %q with tool_call_id %q\n%s",
			callID, resultID, dump)
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
