package host

import (
	"context"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

func TestPersistTurnUsageForwardsToRecorder(t *testing.T) {
	var gotWorkspaceID, gotSessionID string
	var gotUsage ocsessions.Usage
	var gotAt time.Time
	h := &Host{
		workspaceID: "ws-test",
		usageRecorder: func(
			_ context.Context,
			workspaceID, sessionID string,
			usage ocsessions.Usage,
			at time.Time,
		) error {
			gotWorkspaceID = workspaceID
			gotSessionID = sessionID
			gotUsage = usage
			gotAt = at
			return nil
		},
	}

	want := ocsessions.Usage{
		Model:            "gpt-test",
		InputTokens:      10,
		OutputTokens:     20,
		TotalTokens:      30,
		CacheReadTokens:  3,
		CacheWriteTokens: 2,
		ReasoningTokens:  5,
		LatencyMs:        123,
		Calls:            1,
	}
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	h.persistTurnUsage(context.Background(), "s-test",
		[]usageDelta{{usage: want, at: at}}, want)

	if gotWorkspaceID != "ws-test" {
		t.Fatalf("workspace id = %q, want ws-test", gotWorkspaceID)
	}
	if gotSessionID != "s-test" {
		t.Fatalf("session id = %q, want s-test", gotSessionID)
	}
	if gotUsage != want {
		t.Fatalf("usage = %+v, want %+v", gotUsage, want)
	}
	if !gotAt.Equal(at) {
		t.Fatalf("at = %v, want %v", gotAt, at)
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
			time.Time,
		) error {
			called = true
			return nil
		},
	}

	h.persistTurnUsage(context.Background(), "s-test", []usageDelta{{
		usage: ocsessions.Usage{Model: "gpt-test"},
		at:    time.Now().UTC(),
	}}, ocsessions.Usage{})
	if called {
		t.Fatal("recorder called for a turn with no recorded tokens")
	}
}

func TestPersistTurnUsageKeepsModelAndHourBuckets(t *testing.T) {
	type got struct {
		usage ocsessions.Usage
		at    time.Time
	}
	var recorded []got
	h := &Host{
		workspaceID: "ws-test",
		usageRecorder: func(
			_ context.Context,
			_, _ string,
			usage ocsessions.Usage,
			at time.Time,
		) error {
			recorded = append(recorded, got{usage: usage, at: at})
			return nil
		},
	}
	h10 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	h11 := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
	h.persistTurnUsage(context.Background(), "s-test", []usageDelta{
		{
			usage: ocsessions.Usage{
				Model: "model-a", InputTokens: 100,
				OutputTokens: 20, TotalTokens: 120, Calls: 1,
			},
			at: h10,
		},
		{
			usage: ocsessions.Usage{
				Model: "model-b", InputTokens: 30,
				OutputTokens: 5, TotalTokens: 35, Calls: 1,
			},
			at: h10,
		},
		{
			usage: ocsessions.Usage{
				Model: "model-a", InputTokens: 10,
				OutputTokens: 1, TotalTokens: 11, Calls: 1,
			},
			at: h11,
		},
	}, ocsessions.Usage{
		Model:        "model-a",
		InputTokens:  140,
		OutputTokens: 26,
		TotalTokens:  166,
		Calls:        3,
	})
	if len(recorded) != 3 {
		t.Fatalf("recorder calls = %d, want 3: %+v", len(recorded), recorded)
	}
	if recorded[0].usage.Model != "model-a" ||
		!recorded[0].at.Equal(h10) ||
		recorded[0].usage.InputTokens != 100 {
		t.Fatalf("recorded[0] = %+v", recorded[0])
	}
	if recorded[1].usage.Model != "model-b" ||
		recorded[1].usage.InputTokens != 30 {
		t.Fatalf("recorded[1] = %+v", recorded[1])
	}
	if recorded[2].usage.Model != "model-a" ||
		!recorded[2].at.Equal(h11) ||
		recorded[2].usage.InputTokens != 10 {
		t.Fatalf("recorded[2] = %+v", recorded[2])
	}
}

func TestUsageFromReportMapsInferenceFields(t *testing.T) {
	reasoning := int64(7)
	cacheRead := int64(3)
	cacheWrite := int64(2)
	got := usageFromReport(inference.Usage{
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		LatencyMs:    321,
		Model: inference.ModelRef{
			ID: inference.ModelID{
				Provider: "openai-1",
				Name:     "gpt-test",
			},
		},
		Output: inference.OutputTokenUsage{ReasoningTokens: &reasoning},
		Input: inference.InputTokenUsage{
			CacheReadTokens:  &cacheRead,
			CacheWriteTokens: &cacheWrite,
		},
	})
	want := ocsessions.Usage{
		Model:            "gpt-test",
		InputTokens:      100,
		OutputTokens:     50,
		TotalTokens:      150,
		CacheReadTokens:  3,
		CacheWriteTokens: 2,
		ReasoningTokens:  7,
		LatencyMs:        321,
		Calls:            1,
	}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}

func TestReportUsageBucketsPerModel(t *testing.T) {
	runID := "run-usage-1"
	detail := &runDetail{usageHours: make(map[string]ocsessions.Usage)}
	h := &Host{}
	h.mu.Lock()
	h.runs = map[RunID]*runDetail{RunID(runID): detail}
	h.mu.Unlock()
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{
		Identity: agent.Identity{AgentID: "assistant", RunID: runID},
	})

	report := func(name string, input int64) {
		h.reportUsage(ctx, inference.Usage{
			InputTokens:  input,
			OutputTokens: input / 5,
			TotalTokens:  input + input/5,
			Model: inference.ModelRef{
				ID: inference.ModelID{Provider: "openai-1", Name: name},
			},
		})
	}
	report("model-a", 100)
	report("model-b", 30)
	report("model-a", 10)

	aggregate := h.takeUsage(runID)
	if aggregate.TotalTokens != 168 {
		t.Fatalf("run aggregate = %+v, want 168 total", aggregate)
	}
	deltas := h.takeUsageDeltas(runID)
	if len(deltas) != 2 {
		t.Fatalf("usage deltas = %d, want 2: %+v", len(deltas), deltas)
	}
	if deltas[0].usage.Model != "model-a" ||
		deltas[0].usage.InputTokens != 110 ||
		deltas[0].usage.Calls != 2 {
		t.Fatalf("model-a delta = %+v", deltas[0])
	}
	if deltas[1].usage.Model != "model-b" ||
		deltas[1].usage.InputTokens != 30 ||
		deltas[1].usage.Calls != 1 {
		t.Fatalf("model-b delta = %+v", deltas[1])
	}
}

func TestReportUsageKeepsEmptyModelReportInAggregate(t *testing.T) {
	runID := "run-usage-empty-model"
	detail := &runDetail{usageHours: make(map[string]ocsessions.Usage)}
	h := &Host{}
	h.mu.Lock()
	h.runs = map[RunID]*runDetail{RunID(runID): detail}
	h.mu.Unlock()
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{
		Identity: agent.Identity{AgentID: "assistant", RunID: runID},
	})

	h.reportUsage(ctx, inference.Usage{
		InputTokens:  40,
		OutputTokens: 10,
		TotalTokens:  50,
	})

	// The empty-model report cannot be bucketed per model, but it must
	// survive in the run aggregate that persistTurnUsage writes to the
	// session store.
	aggregate := h.takeUsage(runID)
	if aggregate.TotalTokens != 50 || aggregate.Model != "" {
		t.Fatalf("aggregate = %+v, want 50 tokens with no model", aggregate)
	}
	if deltas := h.takeUsageDeltas(runID); len(deltas) != 0 {
		t.Fatalf("usage deltas = %+v, want none", deltas)
	}
}

func TestImportUsageAtUsesEarliestTurn(t *testing.T) {
	earliest := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	later := earliest.Add(2 * time.Hour)
	req := ocsessions.ImportRequest{
		Turns: []ocsessions.ImportTurn{
			{At: later},
			{At: earliest},
		},
	}
	if got := importUsageAt(req); !got.Equal(earliest) {
		t.Fatalf("importUsageAt = %v, want %v", got, earliest)
	}

	before := time.Now().Add(-time.Minute)
	fallback := importUsageAt(ocsessions.ImportRequest{})
	if fallback.Before(before) || fallback.After(time.Now()) {
		t.Fatalf("fallback importUsageAt = %v, want ~now", fallback)
	}
}
