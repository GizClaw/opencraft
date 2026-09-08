package host_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

func startFakeHost(
	t *testing.T, provider *fakeprovider.Server,
) *host.Host {
	t.Helper()
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	h, err := mgr.Acquire(
		context.Background(), workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func discardSink() agent.StreamSink {
	return agent.StreamSinkFunc(func(
		context.Context, event.Envelope,
		agent.StreamDeltaPayload,
	) error {
		return nil
	})
}

// TestHostTurnCorrelationIDsSurfaceFromStream verifies the finish-delta
// request/response ids ride all the way from the provider stream
// through the Host capture into FinishedIDs and the archived turn.
func TestHostTurnCorrelationIDsSurfaceFromStream(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{
		Text:       "done",
		RequestID:  "req-host-1",
		ResponseID: "resp-host-1",
	})
	h := startFakeHost(t, provider)
	ctx := context.Background()

	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "hello"),
		Sink:          discardSink(),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	res, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("wait run: %v", err)
	}
	if res == nil || res.Status != "completed" {
		t.Fatalf("result = %+v, want completed", res)
	}
	reqID, respID := run.FinishedIDs()
	if respID != "resp-host-1" {
		t.Fatalf("FinishedIDs response id = %q, want resp-host-1", respID)
	}
	if reqID != "" && reqID != "req-host-1" {
		t.Fatalf("FinishedIDs request id = %q, want req-host-1 or empty", reqID)
	}
	turn, err := h.Sessions().TurnByRunID(
		ctx, run.ContextID(), run.RunID())
	if err != nil {
		t.Fatalf("TurnByRunID: %v", err)
	}
	if turn.ResponseID != "resp-host-1" {
		t.Fatalf("archived response id = %q, want resp-host-1",
			turn.ResponseID)
	}
	if turn.RequestID != "" && turn.RequestID != "req-host-1" {
		t.Fatalf("archived request id = %q, want req-host-1 or empty",
			turn.RequestID)
	}
}

// TestHostTurnCorrelationIDSurfacesOnProviderFailure verifies the
// request id carried by the provider error chain reaches FinishedIDs
// and the archived failed turn.
func TestHostTurnCorrelationIDSurfacesOnProviderFailure(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{
		Status:    http.StatusBadRequest,
		Error:     "fake provider boom",
		RequestID: "req-fail-1",
	})
	h := startFakeHost(t, provider)
	ctx := context.Background()

	run, err := h.StartRun(ctx, host.RunOptions{
		Message:       message.NewTextMessage(message.RoleUser, "boom"),
		Sink:          discardSink(),
		SkipAutoTitle: true,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		// A failed provider generation is reported on the result
		// status; an infrastructure-level error also still archives.
		t.Logf("wait run error: %v", err)
	}
	reqID, respID := run.FinishedIDs()
	if reqID != "req-fail-1" {
		t.Fatalf("FinishedIDs request id = %q, want req-fail-1", reqID)
	}
	if respID != "" {
		t.Fatalf("FinishedIDs response id = %q, want empty", respID)
	}
	turn, err := h.Sessions().TurnByRunID(
		ctx, run.ContextID(), run.RunID())
	if err != nil {
		t.Fatalf("TurnByRunID: %v", err)
	}
	if turn.RequestID != "req-fail-1" {
		t.Fatalf("archived request id = %q, want req-fail-1",
			turn.RequestID)
	}
	if turn.ResponseID != "" {
		t.Fatalf("archived response id = %q, want empty", turn.ResponseID)
	}
}

// TestHostConcurrentRunsCaptureCorrelationIDs drives concurrent turns
// on one Host while finish deltas are delivered, so the request/response
// id capture is exercised under the race detector.
func TestHostConcurrentRunsCaptureCorrelationIDs(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{
		Text:       "done",
		RequestID:  "req-shared",
		ResponseID: "resp-shared",
	})
	h := startFakeHost(t, provider)
	ctx := context.Background()

	type outcome struct {
		runID, conversationID, reqID, respID string
	}
	done := make(chan outcome, 3)
	for _, prompt := range []string{"alpha", "beta", "gamma"} {
		prompt := prompt
		go func() {
			run, err := h.StartRun(ctx, host.RunOptions{
				Message: message.NewTextMessage(
					message.RoleUser, prompt),
				Sink:          discardSink(),
				SkipAutoTitle: true,
			})
			if err != nil {
				t.Errorf("start run %q: %v", prompt, err)
				done <- outcome{}
				return
			}
			if _, err := run.Wait(ctx); err != nil {
				t.Errorf("wait run %q: %v", prompt, err)
			}
			reqID, respID := run.FinishedIDs()
			done <- outcome{
				runID:          run.RunID(),
				conversationID: run.ContextID(),
				reqID:          reqID,
				respID:         respID,
			}
		}()
	}
	for range 3 {
		got := <-done
		if got.runID == "" {
			continue
		}
		if got.respID != "resp-shared" {
			t.Fatalf("run %s FinishedIDs response id = %q, want resp-shared",
				got.runID, got.respID)
		}
		if got.reqID != "" && got.reqID != "req-shared" {
			t.Fatalf("run %s request id = %q, want req-shared or empty",
				got.runID, got.reqID)
		}
		turn, err := h.Sessions().TurnByRunID(
			ctx, got.conversationID, got.runID)
		if err != nil {
			t.Fatalf("TurnByRunID(%s): %v", got.runID, err)
		}
		if turn.ResponseID != "resp-shared" {
			t.Fatalf("run %s archived ids = %q/%q",
				got.runID, turn.RequestID, turn.ResponseID)
		}
		if turn.RequestID != "" && turn.RequestID != "req-shared" {
			t.Fatalf("run %s archived request id = %q, want req-shared or empty",
				got.runID, turn.RequestID)
		}
	}
}
