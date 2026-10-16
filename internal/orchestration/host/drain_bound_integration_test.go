package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/inference/inferencetest"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// acquireDrainHost builds a Host backed by a scripted provider under a
// throwaway data dir, for teardown tests that need a run parked inside
// the provider.
func acquireDrainHost(
	t *testing.T, provider *inferencetest.ScriptedOpenAI,
) (*Host, context.Context) {
	t.Helper()
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := []byte("version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(
		filepath.Join(configDir, "opencraft.yaml"), seed, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      "openai",
			Name:      "fake",
			API:       "chat",
			Endpoint:  provider.URL(),
			Enabled:   true,
			KeySource: config.KeyLiteral,
			KeyValue:  "test-key",
			Models:    []config.Model{{Name: "fake-model"}},
		}},
	}
	if err := configseed.Write(configDir, cfg); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mgr := NewManagerAt(dataDir, configDir)
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, ctx
}

// waitForRecord polls the captured telemetry for one log body.
func waitForRecord(
	recorder *logcapture.Recorder, body string, timeout time.Duration,
) bool {
	deadline := time.Now().Add(timeout)
	for {
		for _, record := range recorder.Records() {
			if record.Body().AsString() == body {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestHostCloseBoundsTheDrainWait pins the teardown window: closing a
// Host with a turn parked in the provider must not wait for the drain
// forever. Drain runs on the turn-end path, so an unbounded wait lets
// one unsettleable session hold a turn-end RPC — the close gives up at
// hostDrainBudget, reports it, and the runs-set guard (not the drain)
// is what keeps the store safe.
func TestHostCloseBoundsTheDrainWait(t *testing.T) {
	recorder := logcapture.Install(t)
	provider := inferencetest.NewScriptedOpenAI(t,
		inferencetest.ScriptedReply{Text: "done"},
	)
	hold := provider.HoldNext()
	defer hold.Release()

	oldBudget := hostDrainBudget
	hostDrainBudget = 250 * time.Millisecond
	t.Cleanup(func() { hostDrainBudget = oldBudget })

	h, ctx := acquireDrainHost(t, provider)
	run, err := h.StartRun(ctx, RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "park the turn"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	select {
	case <-hold.Ready():
	case <-time.After(30 * time.Second):
		t.Fatal("the provider call never reached the gate")
	}

	if err := h.Close(); err != nil {
		t.Fatalf("close host: %v", err)
	}
	if !waitForRecord(recorder, "host: drain before close failed", 15*time.Second) {
		t.Fatal("the close never reported a bounded drain")
	}
	// The turn was still live the whole time: the budget, not a
	// finished drain, is what let the close move on.
	if err := h.CancelRun(run.RunID()); err != nil {
		t.Fatalf("parked run should still be live: %v", err)
	}
	// Releasing the run lets the deferred teardown finish.
	run.Close()
}
