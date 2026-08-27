package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/rollout"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

func TestOnStreamRolloutRecordsEvents(t *testing.T) {
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		sessions:    store,
		runConvs:    map[string]string{"r1": "s-1"},
		rollouts:    map[string]*rollout.Recorder{},
		rolloutBufs: map[string]*rolloutBuffer{},
	}

	app.onStreamRollout(context.Background(), "r1", agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolCallPart{Call: message.ToolCall{
			ID: "c1", Name: "exec_command",
			Arguments: json.RawMessage(`{"command":"ls"}`),
		}},
	})
	app.onStreamRollout(context.Background(), "r1", agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.TextPart{Text: "hello "},
	})
	app.onStreamRollout(context.Background(), "r1", agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.TextPart{Text: "world"},
	})
	app.onStreamRollout(context.Background(), "r1", agent.StreamDeltaPayload{Type: agent.StreamDeltaFinish})
	app.recordTurnEnd(context.Background(), "s-1", "r1", rollout.TypeTurnCompleted, "completed", "", ocsessions.Usage{
		InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
	})

	path, err := store.RolloutPath("s-1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rollout: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("rollout lines = %d, want 4:\n%s", len(lines), data)
	}

	threads := 0
	var assistant, completed string
	for _, line := range lines {
		var ev rollout.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode line %q: %v", line, err)
		}
		switch ev.Type {
		case rollout.TypeThreadStarted:
			threads++
		case rollout.TypeItemToolCall:
			if ev.Tool != "exec_command" || ev.CallID != "c1" {
				t.Fatalf("tool call event = %+v", ev)
			}
		case rollout.TypeItemAssistantMsg:
			assistant = ev.Content
		case rollout.TypeTurnCompleted:
			completed = ev.Status
			if ev.Usage == nil || ev.Usage.TotalTokens != 15 {
				t.Fatalf("completed usage = %+v", ev.Usage)
			}
		}
	}
	if threads != 1 {
		t.Fatalf("thread.started count = %d, want 1", threads)
	}
	if assistant != "hello world" {
		t.Fatalf("assistant text = %q", assistant)
	}
	if completed != "completed" {
		t.Fatalf("turn status = %q", completed)
	}
}

func TestRolloutPathValidation(t *testing.T) {
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RolloutPath("s-../../evil"); err == nil {
		t.Fatal("invalid id must be rejected")
	}
	path, err := store.RolloutPath("s-abcdef0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, filepath.Join("s-abcdef0123456789", "rollout.jsonl")) {
		t.Fatalf("rollout path = %q", path)
	}
}
