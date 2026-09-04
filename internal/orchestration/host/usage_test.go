package host

import (
	"context"
	"testing"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestPersistTurnUsageForwardsToRecorder(t *testing.T) {
	var gotWorkspaceID, gotSessionID string
	var gotUsage ocsessions.Usage
	h := &Host{
		workspaceID: "ws-test",
		usageRecorder: func(
			_ context.Context,
			workspaceID, sessionID string,
			usage ocsessions.Usage,
		) error {
			gotWorkspaceID = workspaceID
			gotSessionID = sessionID
			gotUsage = usage
			return nil
		},
	}

	want := ocsessions.Usage{
		Model:            "openai-1/gpt-test",
		InputTokens:      10,
		OutputTokens:     20,
		TotalTokens:      30,
		CacheReadTokens:  3,
		CacheWriteTokens: 2,
		ReasoningTokens:  5,
		LatencyMs:        123,
	}
	h.persistTurnUsage(context.Background(), "s-test", want)

	if gotWorkspaceID != "ws-test" {
		t.Fatalf("workspace id = %q, want ws-test", gotWorkspaceID)
	}
	if gotSessionID != "s-test" {
		t.Fatalf("session id = %q, want s-test", gotSessionID)
	}
	if gotUsage != want {
		t.Fatalf("usage = %+v, want %+v", gotUsage, want)
	}
}

func TestPersistTurnUsageSkipsZeroUsage(t *testing.T) {
	called := false
	h := &Host{
		workspaceID: "ws-test",
		usageRecorder: func(
			context.Context,
			string, string,
			ocsessions.Usage,
		) error {
			called = true
			return nil
		},
	}

	h.persistTurnUsage(context.Background(), "s-test", ocsessions.Usage{
		Model: "openai-1/gpt-test",
	})
	if called {
		t.Fatal("recorder called for a turn with no recorded tokens")
	}
}
