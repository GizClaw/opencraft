package host_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// runCheckpointIDs returns the engine run checkpoints currently in the
// workspace store. Checkpoint rows are the crash log recovery reads.
func runCheckpointIDs(t *testing.T, h *host.Host) []string {
	t.Helper()
	ids, err := h.Sessions().State().List(context.Background())
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	var runs []string
	for _, id := range ids {
		if strings.HasPrefix(id, "run-") {
			runs = append(runs, id)
		}
	}
	return runs
}

// TestHostRunCheckpointsCarryRecoveryAnnotations pins the L1-4 base: a
// live turn persists one checkpoint per completed wave (they are the
// crash log, not a context source), each carrying the conversation id
// and the original request so a later assembly can reconstruct the
// turn; the checkpoint is dropped once the turn has an archive row.
func TestHostRunCheckpointsCarryRecoveryAnnotations(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"})
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	// Hold the first model call so the turn is provably mid-flight
	// while the checkpoint below is read.
	gate := provider.HoldNext()
	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "crash me later"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	runID := run.RunID()
	select {
	case <-gate.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("provider call never reached the gate")
	}

	deadline := time.Now().Add(5 * time.Second)
	var cp *agent.Checkpoint
	for {
		ids := runCheckpointIDs(t, h)
		for _, id := range ids {
			if id != runID {
				continue
			}
			loaded, err := h.Sessions().State().Load(ctx, id)
			if err != nil {
				t.Fatalf("load checkpoint: %v", err)
			}
			cp = loaded
		}
		if cp != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cp == nil {
		t.Fatalf("no checkpoint for run %s while the turn was gated", runID)
	}
	if got := cp.Attributes["oc.conversation_id"]; got != run.ContextID() {
		t.Fatalf("checkpoint conversation = %q, want %q", got, run.ContextID())
	}
	rawRequest := cp.Attributes["oc.request"]
	if rawRequest == "" {
		t.Fatal("checkpoint carries no oc.request annotation")
	}
	var request agent.Request
	if err := json.Unmarshal([]byte(rawRequest), &request); err != nil {
		t.Fatalf("decode oc.request: %v", err)
	}
	if request.ContextID != run.ContextID() {
		t.Fatalf("request context = %q, want %q", request.ContextID, run.ContextID())
	}
	if got := request.Message.Content.Text(); got != "crash me later" {
		t.Fatalf("request message = %q, want the original user text", got)
	}

	gate.Release()
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("wait run: %v", err)
	}
	if ids := runCheckpointIDs(t, h); len(ids) != 0 {
		t.Fatalf("checkpoints after a committed turn = %v, want none", ids)
	}
	turns, err := h.Sessions().Turns(ctx, run.ContextID())
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if len(turns) != 1 || turns[0].RunID != runID || turns[0].Status != "completed" {
		t.Fatalf("archived turns = %+v, want one completed run %s", turns, runID)
	}
}
