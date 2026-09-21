package state_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
)

func TestOpenMigrateAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.db")
	s := openState(t, path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	openState(t, path)
}

func TestStateServesCheckpointsAndSettings(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions", "session.db")
	s := openState(t, path)

	cp := agent.Checkpoint{
		ExecID:    "run-1",
		Steps:     []string{"step-a"},
		Iteration: 2,
		Board: &agent.BoardSnapshot{
			Vars: map[string]any{"k": "v"},
		},
		Attributes: map[string]string{"graph": "assistant"},
		Timestamp:  time.Now().UTC(),
	}
	if err := s.Save(ctx, cp); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load(ctx, "run-1")
	if err != nil || loaded == nil || loaded.ExecID != "run-1" {
		t.Fatalf("load = %+v, %v", loaded, err)
	}
	if err := s.SetThinkLevel(ctx, "s-1", "medium"); err != nil {
		t.Fatal(err)
	}
	if level, err := s.ThinkLevel(ctx, "s-1"); err != nil || level != "medium" {
		t.Fatalf("think = %q, %v", level, err)
	}
	if err := s.Delete(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointStatsSplitRunsFromSessionState(t *testing.T) {
	ctx := context.Background()
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))

	stats, err := s.CheckpointStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats != (state.CheckpointStats{}) {
		t.Fatalf("empty store stats = %+v", stats)
	}

	write := func(execID string) {
		t.Helper()
		if err := s.Save(ctx, agent.Checkpoint{
			ExecID:    execID,
			Steps:     []string{"step-a"},
			Board:     &agent.BoardSnapshot{Vars: map[string]any{"k": "v"}},
			Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// One assistant run (the crash-recovery log) and one core
	// session-state row: both live in the table, only the first is a run
	// checkpoint.
	write("run-a")
	write("session-s-1")

	stats, err = s.CheckpointStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 2 || stats.Runs != 1 {
		t.Fatalf("stats = %+v, want 2 rows and 1 run", stats)
	}
	if stats.Bytes <= 0 {
		t.Fatalf("stats = %+v, want encoded bytes", stats)
	}
}
