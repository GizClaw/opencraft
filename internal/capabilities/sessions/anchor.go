package sessions

import (
	"time"
)

// usageAnchorStateName is the conversation_state document holding the last
// provider-measured prompt size. It is written at turn end and read by the
// next turn's world-state prepare hook, which exposes it to the graph's
// compaction node.
const usageAnchorStateName = "usage_anchor"

// UsageAnchor is the provider's own measurement of one request: how many
// input tokens a prompt of AnchoredMessages messages cost. The compaction
// node uses it as the base of its next budget estimate and adds the token
// estimate of whatever the conversation appended since. A character-based
// estimate alone can sit far below the real prompt, and an under-estimate
// is the one direction that fails hard: the graph decides "no fold" and the
// provider rejects the request.
//
// The anchor is a measurement, never a limit. It describes one message
// list, so a fold — which rewrites the channel prefix — invalidates it
// (CompactCount records which fold generation it measured).
type UsageAnchor struct {
	// InputTokens is the provider-reported input token count of the
	// measured call. Cache reads count: they are prompt tokens.
	InputTokens int64 `json:"input_tokens"`
	// AnchoredMessages is the MainChannel length the measured call saw.
	// Messages appended after that index are estimated, not measured.
	AnchoredMessages int `json:"anchored_messages"`
	// CompactCount is the conversation's completed fold count at the time
	// of the measurement.
	CompactCount int `json:"compact_count"`
	// Model is the serving model's name, kept for diagnostics: tokenizers
	// differ between models, so the anchor is exact only for the model
	// that produced it.
	Model string `json:"model,omitempty"`
	// At is when the measurement was recorded.
	At time.Time `json:"at"`
}

// Valid reports whether the anchor carries a usable measurement.
func (a UsageAnchor) Valid() bool {
	return a.InputTokens > 0 && a.AnchoredMessages > 0
}

// WriteUsageAnchor persists one usage anchor for the conversation. An
// anchor without a measurement is dropped instead of stored, so a turn
// whose provider reported no usage cannot shadow a usable older one with
// zeros.
func (s *Store) WriteUsageAnchor(id string, anchor UsageAnchor) error {
	if s == nil || !anchor.Valid() {
		return nil
	}
	return s.WriteState(id, usageAnchorStateName, anchor)
}

// ReadUsageAnchor loads the conversation's last usage anchor. A missing
// document returns os.ErrNotExist, matching ReadState: callers treat it as
// "no measurement yet" and fall back to the character estimate.
func (s *Store) ReadUsageAnchor(id string) (UsageAnchor, error) {
	var anchor UsageAnchor
	if s == nil {
		return UsageAnchor{}, nil
	}
	if err := s.ReadState(id, usageAnchorStateName, &anchor); err != nil {
		return UsageAnchor{}, err
	}
	if !anchor.Valid() {
		return UsageAnchor{}, nil
	}
	return anchor, nil
}
