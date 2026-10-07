package core

import (
	"encoding/json"
	"testing"
	"time"
)

// TestScopedEventWireShape pins how a UI event names the scope it
// belongs to: the application id is present when there is one and
// omitted (not sent as an empty string) for a workspace, which is what
// the frontend routes on. The two scopes share one conversation-id
// generator, so a consumer cannot tell them apart from the id alone.
func TestScopedEventWireShape(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event any
		appID any
	}{
		{
			name: "artifact from an application",
			event: ArtifactEvent{
				AppID:          "hello",
				ConversationID: "s-1",
				RunID:          "run-1",
				Path:           "notes/hello.md",
				Bytes:          17,
			},
			appID: "hello",
		},
		{
			name: "artifact from a workspace",
			event: ArtifactEvent{
				ConversationID: "s-1",
				RunID:          "run-1",
				Path:           "notes/hello.md",
				Bytes:          17,
			},
			appID: nil,
		},
		{
			name:  "session update from an application",
			event: SessionUpdatedEvent{AppID: "hello", ID: "s-1"},
			appID: "hello",
		},
		{
			name:  "session update from a workspace",
			event: SessionUpdatedEvent{ID: "s-1"},
			appID: nil,
		},
		{
			name:  "usage from an application",
			event: UsageEvent{AppID: "hello", Model: "fake-model"},
			appID: "hello",
		},
		{
			name:  "usage from a workspace",
			event: UsageEvent{Model: "fake-model"},
			appID: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.event)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got["app_id"] != tc.appID {
				t.Fatalf("app_id = %v, want %v (%s)",
					got["app_id"], tc.appID, raw)
			}
		})
	}
}

// TestTurnEndEventNamesItsApplication covers the terminal event of an
// application turn: the application page folds the turn by run id, and
// so does the assistant's window for a workspace turn with the same run
// id shape, which is why the event has to say which one it ends.
func TestTurnEndEventNamesItsApplication(t *testing.T) {
	raw, err := json.Marshal(NewTurnEnd(
		"run-1", "hello", "s-1", "completed", "", "", "", "done",
		time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC), 1200, nil,
	))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["app_id"] != "hello" {
		t.Fatalf("app_id = %v, want hello (%s)", got["app_id"], raw)
	}
	if got["run_id"] != "run-1" || got["conversation_id"] != "s-1" {
		t.Fatalf("turn end lost its identity: %s", raw)
	}
}
