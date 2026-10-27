package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
)

// The checkpoint journal keeps one base record per run plus an append-only
// chain: the base carries the whole board once, each delta only what the
// board wrote since the previous boundary. These cases pin the four things
// the engine relies on: a load is base-plus-chain, a chain without a base
// (or at its cap) asks for a base, a new base discards the old chain, and a
// broken chain reads as unavailable rather than as partial state.

func baseCheckpoint(execID string, vars map[string]any) agent.Checkpoint {
	return agent.Checkpoint{
		ExecID:    execID,
		Steps:     []string{"world"},
		Iteration: 1,
		Board:     &agent.BoardSnapshot{Vars: vars},
		Timestamp: time.Now().UTC(),
	}
}

// deltaFor builds a real delta the way the graph does: mark the board, write
// to it, then hand the write set to CheckpointMeta.Delta.
func deltaFor(t *testing.T, execID string, write func(*agent.Board)) agent.CheckpointDelta {
	t.Helper()
	board := agent.NewBoard()
	mark := board.Mark()
	write(board)
	writes := board.Since(mark)
	if writes == nil {
		t.Fatalf("board since mark is nil for %s", execID)
	}
	meta := agent.CheckpointMeta{Steps: []string{"tools"}, Iteration: 2, Timestamp: time.Now().UTC()}
	return meta.Delta(execID, writes)
}

func TestCheckpointJournalFoldsTheChainOntoTheBase(t *testing.T) {
	ctx := context.Background()
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))

	if err := s.WriteBase(ctx, baseCheckpoint("run-j", map[string]any{"a": 1})); err != nil {
		t.Fatalf("write base: %v", err)
	}
	if err := s.Append(ctx, deltaFor(t, "run-j", func(b *agent.Board) {
		b.SetVar("b", 2)
	})); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Append(ctx, deltaFor(t, "run-j", func(b *agent.Board) {
		b.SetVar("c", 3)
	})); err != nil {
		t.Fatalf("append second: %v", err)
	}

	loaded, err := s.Load(ctx, "run-j")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil || loaded.Board == nil {
		t.Fatal("load returned no board")
	}
	for key, want := range map[string]float64{"a": 1, "b": 2, "c": 3} {
		got, ok := loaded.Board.Vars[key]
		if !ok {
			t.Fatalf("var %q missing after replay: %+v", key, loaded.Board.Vars)
		}
		if asFloat, ok := got.(float64); !ok || asFloat != want {
			t.Fatalf("var %q = %#v, want %v", key, got, want)
		}
	}
	// The boundary metadata follows the newest delta.
	if loaded.Iteration != 2 {
		t.Fatalf("iteration = %d, want the latest delta's 2", loaded.Iteration)
	}
}

func TestCheckpointJournalAsksForABaseWithoutOne(t *testing.T) {
	ctx := context.Background()
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	err := s.Append(ctx, deltaFor(t, "run-nobase", func(b *agent.Board) {
		b.SetVar("b", 2)
	}))
	if !errors.Is(err, agent.ErrNeedsBase) {
		t.Fatalf("append without a base = %v, want ErrNeedsBase", err)
	}
}

func TestCheckpointJournalAsksForABaseAtItsCap(t *testing.T) {
	ctx := context.Background()
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	if err := s.WriteBase(ctx, baseCheckpoint("run-cap", map[string]any{"a": 1})); err != nil {
		t.Fatalf("write base: %v", err)
	}
	// The cap matches core's default compaction window; the store is the
	// backstop for a writer that never compacts.
	const cap = 32
	for i := 0; i < cap; i++ {
		if err := s.Append(ctx, deltaFor(t, "run-cap", func(b *agent.Board) {
			b.SetVar("n", i)
		})); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	err := s.Append(ctx, deltaFor(t, "run-cap", func(b *agent.Board) {
		b.SetVar("n", cap)
	}))
	if !errors.Is(err, agent.ErrNeedsBase) {
		t.Fatalf("append past the cap = %v, want ErrNeedsBase", err)
	}
}

func TestCheckpointWriteBaseDiscardsTheChain(t *testing.T) {
	ctx := context.Background()
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	if err := s.WriteBase(ctx, baseCheckpoint("run-reset", map[string]any{"a": 1})); err != nil {
		t.Fatalf("write base: %v", err)
	}
	if err := s.Append(ctx, deltaFor(t, "run-reset", func(b *agent.Board) {
		b.SetVar("b", 2)
	})); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.WriteBase(ctx, baseCheckpoint("run-reset", map[string]any{"a": 9})); err != nil {
		t.Fatalf("second base: %v", err)
	}
	loaded, err := s.Load(ctx, "run-reset")
	if err != nil || loaded == nil {
		t.Fatalf("load = %+v, %v", loaded, err)
	}
	if _, ok := loaded.Board.Vars["b"]; ok {
		t.Fatalf("the discarded chain is still applied: %+v", loaded.Board.Vars)
	}
	if got, _ := loaded.Board.Vars["a"].(float64); got != 9 {
		t.Fatalf("base var a = %#v, want the new base's 9", loaded.Board.Vars["a"])
	}
}

func TestCheckpointDeleteRemovesTheChain(t *testing.T) {
	ctx := context.Background()
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	if err := s.WriteBase(ctx, baseCheckpoint("run-del", map[string]any{"a": 1})); err != nil {
		t.Fatalf("write base: %v", err)
	}
	if err := s.Append(ctx, deltaFor(t, "run-del", func(b *agent.Board) {
		b.SetVar("b", 2)
	})); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Delete(ctx, "run-del"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	loaded, err := s.Load(ctx, "run-del")
	if err != nil || loaded != nil {
		t.Fatalf("load after delete = %+v, %v; want nothing left", loaded, err)
	}
	// A delta appended to the gone base must not resurrect the record.
	err = s.Append(ctx, deltaFor(t, "run-del", func(b *agent.Board) {
		b.SetVar("c", 3)
	}))
	if !errors.Is(err, agent.ErrNeedsBase) {
		t.Fatalf("append after delete = %v, want ErrNeedsBase", err)
	}
}

func TestCheckpointChainGapReadsAsUnavailable(t *testing.T) {
	ctx := context.Background()
	s := openState(t, filepath.Join(t.TempDir(), "session.db"))
	if err := s.WriteBase(ctx, baseCheckpoint("run-gap", map[string]any{"a": 1})); err != nil {
		t.Fatalf("write base: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Append(ctx, deltaFor(t, "run-gap", func(b *agent.Board) {
			b.SetVar("n", i)
		})); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// Drop the first delta: the chain no longer describes consecutive
	// boundaries, and replaying what is left would silently lose state.
	if _, err := s.Handle().SQLDB().ExecContext(ctx,
		`DELETE FROM agent_checkpoint_deltas WHERE exec_id = ? AND seq = 1`,
		"run-gap"); err != nil {
		t.Fatalf("corrupt chain: %v", err)
	}
	if _, err := s.Load(ctx, "run-gap"); err == nil {
		t.Fatal("load of a gapped chain succeeded, want an unavailable error")
	}
}
