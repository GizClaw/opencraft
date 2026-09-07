package sessions

import "github.com/GizClaw/flowcraft/core/inference"

// UsageFromReport maps one flowcraft inference usage report to the
// session usage delta shape used by sessions.Store, the user-level
// usage tables, rollout audit events, and the desktop UI. Model
// statistics bucket by model name only, so the provider prefix is
// dropped.
func UsageFromReport(u inference.Usage) Usage {
	out := Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
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
