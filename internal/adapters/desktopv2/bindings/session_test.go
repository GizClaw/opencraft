package bindings

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestSessionMetaJSONShape(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	dto := toSessionMeta(sessions.Meta{
		ID:        "s-1",
		Title:     "hello",
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  2,
		Usage:     sessions.Usage{TotalTokens: 42},
	})
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"id", "title", "created_at", "updated_at", "messages", "total_tokens",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("session meta JSON missing %q: %s", key, raw)
		}
	}
	if got["total_tokens"].(float64) != 42 {
		t.Fatalf("total_tokens = %v, want 42", got["total_tokens"])
	}
}
