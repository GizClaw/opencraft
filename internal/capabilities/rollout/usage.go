package rollout

import "github.com/GizClaw/opencraft/internal/capabilities/sessions"

// FromUsage projects the shared session usage snapshot into the flat
// rollout audit shape. It exists once so turn-end recording and any
// future JSONL consumers cannot drift field mappings.
func FromUsage(u sessions.Usage) Usage {
	return Usage{
		InputTokens:     u.InputTokens,
		OutputTokens:    u.OutputTokens,
		CacheReadTokens: u.CacheReadTokens,
		ReasoningTokens: u.ReasoningTokens,
		TotalTokens:     u.TotalTokens,
		LatencyMs:       u.LatencyMs,
	}
}
