// Post-turn review settings.
//
// This file owns the review resource's settings shape, its defaults and
// validation, and the user-layer persistence the memory tab uses. The
// opencraft.review observe hook decodes its factory settings with these
// types, so the page and the runtime cannot drift.
//
// The default is off: a review spends a model call, and its output is
// only trustworthy once the user has watched a few of its suggestions.
package config

import (
	"fmt"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// ResourceReview is the deploy-document id of the review resource.
const ResourceReview = "review"

// Bounds and defaults for the review knobs.
const (
	ReviewDefaultEveryTurns     = 5
	ReviewMinEveryTurns         = 1
	ReviewMaxEveryTurns         = 100
	ReviewDefaultMinToolCalls   = 4
	ReviewMinMinToolCalls       = 0
	ReviewMaxMinToolCalls       = 100
	ReviewDefaultMaxSuggestions = 3
	ReviewMinMaxSuggestions     = 1
	ReviewMaxMaxSuggestions     = 10
	ReviewDefaultTimeoutSeconds = 90
	ReviewMinTimeoutSeconds     = 15
	ReviewMaxTimeoutSeconds     = 600
)

// ReviewSettings is the review resource's settings subtree.
type ReviewSettings struct {
	// Enabled is the master switch (default off: observe the quality of
	// the suggestions before letting the review run unattended).
	Enabled *bool `json:"enabled,omitempty"`
	// EveryTurns runs a review on every Nth completed turn of a
	// conversation. An absent (zero) value takes the default cadence;
	// the accepted range is ReviewMinEveryTurns..ReviewMaxEveryTurns.
	// Switching the review off is what Enabled is for.
	EveryTurns int `json:"every_turns,omitempty"`
	// MinToolCalls skips turns that used fewer tools than this: a turn
	// that only chatted rarely yields something worth remembering.
	MinToolCalls int `json:"min_tool_calls,omitempty"`
	// OnFailure runs a review after a failed or interrupted turn even
	// when the cadence does not call for one.
	OnFailure bool `json:"on_failure,omitempty"`
	// MaxSuggestions caps how many candidates one review may queue.
	MaxSuggestions int `json:"max_suggestions,omitempty"`
	// TimeoutSeconds bounds the whole review (model call included).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// ReviewConfig is the validated, defaulted view.
type ReviewConfig struct {
	Enabled        bool
	EveryTurns     int
	MinToolCalls   int
	OnFailure      bool
	MaxSuggestions int
	TimeoutSeconds int
}

// DefaultReviewSettings returns the shipped settings.
func DefaultReviewSettings() ReviewSettings {
	enabled := false
	return ReviewSettings{
		Enabled:        &enabled,
		EveryTurns:     ReviewDefaultEveryTurns,
		MinToolCalls:   ReviewDefaultMinToolCalls,
		OnFailure:      true,
		MaxSuggestions: ReviewDefaultMaxSuggestions,
		TimeoutSeconds: ReviewDefaultTimeoutSeconds,
	}
}

// Resolve validates the settings and applies defaults.
func (s ReviewSettings) Resolve() (ReviewConfig, error) {
	out := ReviewConfig{
		Enabled:        s.Enabled != nil && *s.Enabled,
		EveryTurns:     s.EveryTurns,
		MinToolCalls:   s.MinToolCalls,
		OnFailure:      s.OnFailure,
		MaxSuggestions: s.MaxSuggestions,
		TimeoutSeconds: s.TimeoutSeconds,
	}
	if out.EveryTurns == 0 {
		out.EveryTurns = ReviewDefaultEveryTurns
	}
	if out.EveryTurns < ReviewMinEveryTurns ||
		out.EveryTurns > ReviewMaxEveryTurns {
		return ReviewConfig{}, fmt.Errorf(
			"review: every_turns %d out of range (%d-%d)",
			out.EveryTurns, ReviewMinEveryTurns, ReviewMaxEveryTurns)
	}
	if out.MinToolCalls < ReviewMinMinToolCalls ||
		out.MinToolCalls > ReviewMaxMinToolCalls {
		return ReviewConfig{}, fmt.Errorf(
			"review: min_tool_calls %d out of range (%d-%d)",
			out.MinToolCalls, ReviewMinMinToolCalls, ReviewMaxMinToolCalls)
	}
	if out.MaxSuggestions == 0 {
		out.MaxSuggestions = ReviewDefaultMaxSuggestions
	}
	if out.MaxSuggestions < ReviewMinMaxSuggestions ||
		out.MaxSuggestions > ReviewMaxMaxSuggestions {
		return ReviewConfig{}, fmt.Errorf(
			"review: max_suggestions %d out of range (%d-%d)",
			out.MaxSuggestions, ReviewMinMaxSuggestions, ReviewMaxMaxSuggestions)
	}
	if out.TimeoutSeconds == 0 {
		out.TimeoutSeconds = ReviewDefaultTimeoutSeconds
	}
	if out.TimeoutSeconds < ReviewMinTimeoutSeconds ||
		out.TimeoutSeconds > ReviewMaxTimeoutSeconds {
		return ReviewConfig{}, fmt.Errorf(
			"review: timeout_seconds %d out of range (%d-%d)",
			out.TimeoutSeconds, ReviewMinTimeoutSeconds, ReviewMaxTimeoutSeconds)
	}
	return out, nil
}

// reviewLayer is the user-layer document SaveReview writes.
type reviewLayer struct {
	Version   string `json:"version"`
	Resources struct {
		Review *reviewResourceLayer `json:"review,omitempty"`
	} `json:"resources"`
}

type reviewResourceLayer struct {
	Settings *ReviewSettings `json:"settings,omitempty"`
}

// LoadReview returns the effective settings: embedded defaults overlaid
// with the user layer's resources.review.settings.
func LoadReview(configDir string) (ReviewSettings, error) {
	return layeredResourceSettings[ReviewSettings](configDir, ResourceReview)
}

// SaveReview validates the settings and persists them as the user
// layer's review resource.
func SaveReview(configDir string, settings ReviewSettings) error {
	if _, err := settings.Resolve(); err != nil {
		return err
	}
	layer := reviewLayer{Version: "v1"}
	// Every field is omitempty, so a zero-value save would render the
	// fatal `settings: {}`; omit the key instead (see nonEmptySettings).
	layer.Resources.Review = &reviewResourceLayer{
		Settings: nonEmptySettings(settings),
	}
	fresh, err := yaml.Marshal(layer)
	if err != nil {
		return fmt.Errorf("config: render review layer: %w", err)
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		map[string]bool{ResourceReview: true},
		map[string]bool{},
		map[string]bool{},
		false,
	)
	if err != nil {
		return err
	}
	return writeFileAtomic(
		filepath.Join(configDir, "opencraft.yaml"),
		merged,
		0o600,
	)
}
