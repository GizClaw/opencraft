package state_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
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
