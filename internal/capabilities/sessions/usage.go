package sessions

import "github.com/GizClaw/flowcraft/core/inference"

// PromptTokens returns the inclusive prompt size of one inference report:
// every prompt token, cache reads and cache writes included.
//
// It exists because providers disagree on their wire counters. OpenAI's
// prompt_tokens already includes the cached tokens (its cache counters are
// subsets of it), while Anthropic's input_tokens counts only the tokens
// that were neither read from nor written to the cache — for a prompt
// served mostly from cache it reports a few hundred tokens next to tens of
// thousands of cache reads. Consumers that treat input tokens as "how big
// was the prompt" (the compaction budget, the cache-hit rate, the usage
// table) therefore need one rule:
//
//	inclusive = input, unless the cache counters overrun it
//
// The overrun test is the discriminator: with inclusive counters the cache
// buckets are subsets and can never exceed the total, so a total smaller
// than their sum is an exclusive-counter report and the buckets are added
// back. The residual ambiguity (an exclusive report whose cache buckets
// happen to be small) under-counts by at most the cached share, in the
// conservative direction for a budget.
//
// flowcraft's drivers normalize this at the wire boundary (see
// InputTokenUsage in core/inference/usage.go); until the pinned driver
// versions carry that normalization, this is where opencraft keeps the two
// conventions apart.
func PromptTokens(u inference.Usage) int64 {
	input := u.InputTokens
	cached := int64(0)
	if u.Input.CacheReadTokens != nil {
		cached += *u.Input.CacheReadTokens
	}
	if u.Input.CacheWriteTokens != nil {
		cached += *u.Input.CacheWriteTokens
	}
	if input <= 0 {
		return 0
	}
	if cached > input {
		return input + cached
	}
	return input
}

// UsageFromReport maps one flowcraft inference usage report to the
// session usage delta shape used by sessions.Store, the user-level
// usage tables, rollout audit events, and the desktop UI. InputTokens is
// the inclusive prompt size (see PromptTokens) so one row means the same
// thing whichever provider produced it; Model statistics bucket by model
// name only, so the provider prefix is dropped.
//
// TotalTokens moves with InputTokens for the same reason: a driver whose
// wire counters are exclusive reports its own total as input + output of
// the same exclusive bucket, so leaving it alone would publish a row whose
// parts do not add up (a cached conversation showing 300 total next to
// 49224 input). The delta is zero for inclusive providers, which is what
// makes this normalization expire on its own once the drivers carry it.
func UsageFromReport(u inference.Usage) Usage {
	inclusive := PromptTokens(u)
	total := u.TotalTokens
	if delta := inclusive - u.InputTokens; delta > 0 {
		total += delta
	}
	out := Usage{
		InputTokens:  inclusive,
		OutputTokens: u.OutputTokens,
		TotalTokens:  total,
		LatencyMs:    u.LatencyMs,
		Calls:        1,
	}
	if u.Model.ID.Name != "" {
		out.Model = NormalizeModelName(u.Model.ID.Name)
	}
	if u.Output.ReasoningTokens != nil {
		out.ReasoningTokens = *u.Output.ReasoningTokens
	}
	if u.Input.CacheReadTokens != nil {
		out.CacheReadTokens = *u.Input.CacheReadTokens
	}
	if u.Input.CacheWriteTokens != nil {
		out.CacheWriteTokens = *u.Input.CacheWriteTokens
	}
	return out
}

// AddUsage accumulates one usage delta onto a running session total,
// keeping the most recent non-empty model attribution.
func AddUsage(base, delta Usage) Usage {
	base.InputTokens += delta.InputTokens
	base.OutputTokens += delta.OutputTokens
	base.TotalTokens += delta.TotalTokens
	base.CacheReadTokens += delta.CacheReadTokens
	base.CacheWriteTokens += delta.CacheWriteTokens
	base.ReasoningTokens += delta.ReasoningTokens
	base.LatencyMs += delta.LatencyMs
	base.Calls += delta.Calls
	if delta.Model != "" {
		base.Model = delta.Model
	}
	return base
}
