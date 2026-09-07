package rollout

import (
	"encoding/json"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestFromUsageMapsFields(t *testing.T) {
	got := FromUsage(sessions.Usage{
		InputTokens:     100,
		OutputTokens:    20,
		TotalTokens:     120,
		CacheReadTokens: 3,
		ReasoningTokens: 5,
		LatencyMs:       45,
		Calls:           2,
	})
	want := Usage{
		InputTokens:     100,
		OutputTokens:    20,
		TotalTokens:     120,
		CacheReadTokens: 3,
		ReasoningTokens: 5,
		LatencyMs:       45,
	}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}

// TestEventJSONGolden locks the rollout audit schema so accidental
// field renames/repurposing show up in review instead of silently
// changing existing consumers.
func TestEventJSONGolden(t *testing.T) {
	usage := FromUsage(sessions.Usage{
		InputTokens:     100,
		OutputTokens:    20,
		TotalTokens:     120,
		CacheReadTokens: 3,
		ReasoningTokens: 5,
		LatencyMs:       45,
	})
	ev := Event{
		Type:           TypeTurnCompleted,
		Seq:            7,
		Time:           "2026-09-07T10:00:00Z",
		ConversationID: "s-1",
		RunID:          "r-1",
		Status:         "completed",
		Usage:          &usage,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"turn.completed","seq":7,"time":"2026-09-07T10:00:00Z","conversation_id":"s-1","run_id":"r-1","status":"completed","usage":{"input_tokens":100,"output_tokens":20,"cache_read_tokens":3,"reasoning_tokens":5,"total_tokens":120,"latency_ms":45}}`
	if string(data) != want {
		t.Fatalf("golden JSON mismatch:\n got %s\nwant %s", data, want)
	}
}
