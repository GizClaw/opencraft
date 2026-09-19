package sessions

import (
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"
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
		Model: model.ModelRef{
			ID: model.ModelID{
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

// TestPromptTokensKeepsInclusiveCounters pins the ordinary shape: OpenAI's
// prompt_tokens already includes the cached tokens, so the cache buckets
// are subsets and the total passes through untouched.
func TestPromptTokensKeepsInclusiveCounters(t *testing.T) {
	cached := int64(48000)
	uncached := int64(1224)
	usage := inference.Usage{
		InputTokens: 49224,
		Input: inference.InputTokenUsage{
			CacheReadTokens: &cached,
			UncachedTokens:  &uncached,
		},
	}
	if got := PromptTokens(usage); got != 49224 {
		t.Fatalf("prompt tokens = %d, want the inclusive total 49224", got)
	}
}

// TestPromptTokensAddsBackExclusiveCounters pins the Anthropic shape: its
// input_tokens counts only the tokens that were neither read from nor
// written to the cache, so a prompt served mostly from cache reports a
// total smaller than its own cache buckets. Passing that number on as the
// prompt size would size a cached conversation as if it were nearly empty,
// which is how a compaction budget stops folding.
func TestPromptTokensAddsBackExclusiveCounters(t *testing.T) {
	cached := int64(48000)
	written := int64(1024)
	usage := inference.Usage{
		InputTokens: 200,
		Input: inference.InputTokenUsage{
			CacheReadTokens:  &cached,
			CacheWriteTokens: &written,
		},
	}
	if got := PromptTokens(usage); got != 49224 {
		t.Fatalf("prompt tokens = %d, want input + cache read + cache write", got)
	}
}

// TestPromptTokensWithoutCountersKeepsInput pins the two degenerate
// shapes: no cache counters at all (nothing to add), and no usage
// reported (zero, never a guess).
func TestPromptTokensWithoutCountersKeepsInput(t *testing.T) {
	if got := PromptTokens(inference.Usage{InputTokens: 900}); got != 900 {
		t.Fatalf("prompt tokens = %d, want the plain input 900", got)
	}
	if got := PromptTokens(inference.Usage{}); got != 0 {
		t.Fatalf("prompt tokens = %d, want 0 when nothing was reported", got)
	}
}

// TestUsageFromReportStoresInclusiveInput pins the stored column: one row
// means the same thing whichever provider produced it, so a cache-hit rate
// computed as cache_read / input_tokens stays a ratio.
func TestUsageFromReportStoresInclusiveInput(t *testing.T) {
	cached := int64(48000)
	got := UsageFromReport(inference.Usage{
		InputTokens:  200,
		OutputTokens: 100,
		// The wire total of an exclusive-counter provider: input + output
		// of the exclusive bucket. Stored as-is it would contradict the
		// input column (and the usage hero renders the total as the
		// headline number), so it moves with the input.
		TotalTokens: 300,
		Input:       inference.InputTokenUsage{CacheReadTokens: &cached},
	})
	if got.InputTokens != 48200 {
		t.Fatalf("stored input tokens = %d, want the inclusive 48200", got.InputTokens)
	}
	if got.TotalTokens != 48300 {
		t.Fatalf("stored total tokens = %d, want 48300 (input + output)", got.TotalTokens)
	}
	if got.CacheReadTokens != 48000 {
		t.Fatalf("stored cache read = %d, want 48000", got.CacheReadTokens)
	}
	// The invariant the usage table renders: total = input + output.
	if got.TotalTokens != got.InputTokens+got.OutputTokens {
		t.Fatalf("total %d != input %d + output %d",
			got.TotalTokens, got.InputTokens, got.OutputTokens)
	}
}

// TestUsageFromReportKeepsInclusiveTotal pins the other side of the same
// rule: a provider whose prompt_tokens already includes the cached tokens
// reports a total that agrees with the input column, so it passes through
// untouched (no double counting).
func TestUsageFromReportKeepsInclusiveTotal(t *testing.T) {
	cached := int64(48000)
	got := UsageFromReport(inference.Usage{
		InputTokens:  49224,
		OutputTokens: 100,
		TotalTokens:  49324,
		Input:        inference.InputTokenUsage{CacheReadTokens: &cached},
	})
	if got.InputTokens != 49224 || got.TotalTokens != 49324 {
		t.Fatalf("usage = %+v, want the reported inclusive numbers untouched", got)
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
