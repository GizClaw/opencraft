package sessions

import (
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
)

func TestUsageFromReportMapsInferenceFields(t *testing.T) {
	reasoning := int64(7)
	cacheRead := int64(3)
	cacheWrite := int64(2)
	got := UsageFromReport(inference.Usage{
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
	want := Usage{
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

func TestAddUsageAccumulatesAndKeepsLastModel(t *testing.T) {
	base := Usage{
		Model:            "a",
		InputTokens:      10,
		OutputTokens:     5,
		TotalTokens:      15,
		CacheReadTokens:  1,
		CacheWriteTokens: 2,
		ReasoningTokens:  3,
		LatencyMs:        100,
		Calls:            2,
	}
	got := AddUsage(base, Usage{
		Model:        "b",
		InputTokens:  20,
		OutputTokens: 10,
		TotalTokens:  30,
		Calls:        3,
	})
	want := Usage{
		Model:            "b",
		InputTokens:      30,
		OutputTokens:     15,
		TotalTokens:      45,
		CacheReadTokens:  1,
		CacheWriteTokens: 2,
		ReasoningTokens:  3,
		LatencyMs:        100,
		Calls:            5,
	}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}
