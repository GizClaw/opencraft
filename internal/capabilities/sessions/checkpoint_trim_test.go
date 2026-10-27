package sessions

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// TestStoreCheckpointTrimmer pins the checkpoint shrink hook: Save
// persists what the installed trimmer returns, a nil trimmer restores
// persistence of the checkpoint as handed over, and the trimmer can be
// replaced at any time (a runtime reload re-decides it).
func TestStoreCheckpointTrimmer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := newMigratedStore(root, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.CloseDB() }()
	ctx := context.Background()

	full := agent.Checkpoint{
		ExecID:    "run-trim",
		Steps:     []string{"world"},
		Iteration: 1,
		Board: &agent.BoardSnapshot{
			Vars: map[string]any{"keep": "yes"},
			Channels: map[string][]message.Message{
				agent.MainChannel: {
					message.NewTextMessage(message.RoleUser, "hi"),
					message.NewTextMessage(message.RoleAssistant, "hello"),
				},
			},
		},
		Attributes: map[string]string{"oc.conversation_id": "s-1"},
		Timestamp:  time.Now().UTC(),
	}

	trimmed := full.Clone()
	trimmed.Board = &agent.BoardSnapshot{
		Vars: map[string]any{"trimmed": true},
		Channels: map[string][]message.Message{
			agent.MainChannel: {message.NewTextMessage(message.RoleUser, "hi")},
		},
	}

	calls := 0
	store.SetCheckpointTrimmer(func(_ context.Context, cp agent.Checkpoint) agent.Checkpoint {
		calls++
		if cp.ExecID != full.ExecID {
			t.Errorf("trimmer saw exec id %q, want %q", cp.ExecID, full.ExecID)
		}
		return trimmed
	})
	if err := store.Save(ctx, full); err != nil {
		t.Fatalf("save with trimmer: %v", err)
	}
	if calls != 1 {
		t.Fatalf("trimmer calls = %d, want 1", calls)
	}
	loaded, err := store.Load(ctx, full.ExecID)
	if err != nil || loaded == nil {
		t.Fatalf("load = %+v, %v", loaded, err)
	}
	if got, ok := loaded.Board.Vars["trimmed"]; !ok || got != true {
		t.Fatalf("persisted vars = %v, want the trimmed board", loaded.Board.Vars)
	}
	if got := len(loaded.Board.Channels[agent.MainChannel]); got != 1 {
		t.Fatalf("persisted channel = %d messages, want the trimmed one", got)
	}
	// The trimmer shrinks the board only: the recovery annotations and
	// the position markers ride along untouched.
	if loaded.Attributes["oc.conversation_id"] != "s-1" || len(loaded.Steps) != 1 ||
		loaded.Iteration != 1 {
		t.Fatalf("trimmed checkpoint lost recovery data: %+v", loaded)
	}
	if loaded.ExecID != full.ExecID {
		t.Fatalf("exec id = %q, want %q", loaded.ExecID, full.ExecID)
	}

	store.SetCheckpointTrimmer(nil)
	if err := store.Save(ctx, full); err != nil {
		t.Fatalf("save without trimmer: %v", err)
	}
	if calls != 1 {
		t.Fatalf("trimmer calls after removal = %d, want 1", calls)
	}
	loaded, err = store.Load(ctx, full.ExecID)
	if err != nil || loaded == nil {
		t.Fatalf("load = %+v, %v", loaded, err)
	}
	if got := len(loaded.Board.Channels[agent.MainChannel]); got != 2 {
		t.Fatalf("persisted channel = %d messages, want the untrimmed board", got)
	}
	raw, err := json.Marshal(loaded.Board)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("empty board persisted")
	}
}

// TestStoreTrimmerAppliesToABase pins the other half of the trimmer's
// contract after the journalling store: a base record is the one record that
// carries the whole board, so it is the one the trimmer shrinks. The deltas
// that follow only carry writes and are stored as they arrive.
func TestStoreTrimmerAppliesToABase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := newMigratedStore(root, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.CloseDB() }()
	ctx := context.Background()

	store.SetCheckpointTrimmer(func(_ context.Context, cp agent.Checkpoint) agent.Checkpoint {
		cp.Attributes = map[string]string{"trimmed": "yes"}
		return cp
	})
	full := agent.Checkpoint{
		ExecID: "run-base-trim",
		Steps:  []string{"world"},
		Board: &agent.BoardSnapshot{Vars: map[string]any{
			"keep": "yes",
			"drop": "no",
		}},
		Attributes: map[string]string{"trimmed": "no"},
		Timestamp:  time.Now().UTC(),
	}
	if err := store.WriteBase(ctx, full); err != nil {
		t.Fatalf("write base: %v", err)
	}
	loaded, err := store.Load(ctx, "run-base-trim")
	if err != nil || loaded == nil {
		t.Fatalf("load = %+v, %v", loaded, err)
	}
	if loaded.Attributes["trimmed"] != "yes" {
		t.Fatalf("attributes = %+v, want the trimmer's record", loaded.Attributes)
	}
}
