package bindings

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/review"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// Review is the pending write-back queue as the memory tab consumes it:
// what the post-turn review proposed and the user's verdict on each
// item. The queue never writes a fact from here — accepting a
// suggestion runs the same write path as the remember tool
// (review.ApplyMemory), so dedupe, limits and provenance are applied
// exactly once.
type Review struct {
	core *core.Core
}

// NewReviewBinding builds the review service.
func NewReviewBinding(c *core.Core) *Review { return &Review{core: c} }

// ReviewSuggestionView is one queued suggestion. The decoded fields
// describe the candidate fact; Payload keeps the raw JSON so the page
// can show exactly what was proposed.
type ReviewSuggestionView struct {
	ID                 string `json:"id"`
	CreatedAt          string `json:"created_at,omitempty"`
	UpdatedAt          string `json:"updated_at,omitempty"`
	Status             string `json:"status"`
	Kind               string `json:"kind"`
	Reason             string `json:"reason,omitempty"`
	SourceWorkspace    string `json:"source_workspace,omitempty"`
	SourceConversation string `json:"source_conversation,omitempty"`
	SourceRun          string `json:"source_run,omitempty"`
	// The candidate fact, decoded from Payload.
	Text          string `json:"text,omitempty"`
	Scope         string `json:"scope,omitempty"`
	CandidateKind string `json:"candidate_kind,omitempty"`
	Payload       string `json:"payload,omitempty"`
}

// ReviewState is the whole state of the review settings card: the
// effective settings, the editable bounds, and the queue depth.
type ReviewState struct {
	Enabled      bool `json:"enabled"`
	EveryTurns   int  `json:"every_turns"`
	MinToolCalls int  `json:"min_tool_calls"`
	OnFailure    bool `json:"on_failure"`
	// MaxSuggestions, TimeoutSeconds and OnFailure are read-only here:
	// the page shows them, the deploy document owns them.
	MaxSuggestions int `json:"max_suggestions"`
	TimeoutSeconds int `json:"timeout_seconds"`

	MinEveryTurns       int `json:"min_every_turns"`
	MaxEveryTurns       int `json:"max_every_turns"`
	DefaultEveryTurns   int `json:"default_every_turns"`
	MinMinToolCalls     int `json:"min_min_tool_calls"`
	MaxMinToolCalls     int `json:"max_min_tool_calls"`
	DefaultMinToolCalls int `json:"default_min_tool_calls"`
	MinMaxSuggestions   int `json:"min_max_suggestions"`
	MaxMaxSuggestions   int `json:"max_max_suggestions"`
	MinTimeoutSeconds   int `json:"min_timeout_seconds"`
	MaxTimeoutSeconds   int `json:"max_timeout_seconds"`

	// Available reports whether a user database is open; without one
	// the queue is always empty and decisions are refused.
	Available bool `json:"available"`
	// Pending/Accepted/Discarded are the queue depths.
	Pending   int `json:"pending"`
	Accepted  int `json:"accepted"`
	Discarded int `json:"discarded"`
}

// ReviewSettingsRequest is the save payload of the review card: the
// master switch and the two cadence numbers. The read-only knobs
// (max_suggestions, timeout_seconds, on_failure) are not part of it —
// the user layer cannot represent "on_failure: false" (the field has no
// pointer to tell unset from false), so the page must not offer a
// control that would silently revert.
type ReviewSettingsRequest struct {
	Enabled      bool `json:"enabled"`
	EveryTurns   int  `json:"every_turns"`
	MinToolCalls int  `json:"min_tool_calls"`
}

// ReviewAcceptResult reports what accepting a suggestion wrote: the
// updated row and the fact it produced.
type ReviewAcceptResult struct {
	Suggestion ReviewSuggestionView `json:"suggestion"`
	Fact       MemoryFactView       `json:"fact"`
}

// reviewQueueOf resolves the suggestion queue out of a core, or false
// when this runtime has no user database.
func reviewQueueOf(c *core.Core) (reviewstore.Queue, bool) {
	manager := c.Runtime.Manager()
	if manager == nil {
		return nil, false
	}
	store := manager.ReviewStore()
	if store == nil || store.Empty() {
		return nil, false
	}
	return store, true
}

// reviewQueue is the read-side accessor: reads degrade to an empty
// queue when there is no user database.
func (b *Review) reviewQueue() (reviewstore.Queue, bool) {
	return reviewQueueOf(b.core)
}

// requireReviewQueue is the write-side guard: a decision without a user
// database is refused instead of dropped.
func (b *Review) requireReviewQueue() (reviewstore.Queue, error) {
	queue, ok := b.reviewQueue()
	if !ok {
		return nil, errdefs.NotAvailablef(
			"review: no user database in this runtime")
	}
	return queue, nil
}

// reviewSuggestionView maps one row, decoding the candidate payload so
// the page can render the proposed fact without another round trip.
func reviewSuggestionView(suggestion reviewstore.Suggestion) ReviewSuggestionView {
	out := ReviewSuggestionView{
		ID:                 suggestion.ID,
		CreatedAt:          rfc3339(suggestion.CreatedAt),
		UpdatedAt:          rfc3339(suggestion.UpdatedAt),
		Status:             suggestion.Status,
		Kind:               suggestion.Kind,
		Reason:             suggestion.Reason,
		SourceWorkspace:    suggestion.SourceWorkspace,
		SourceConversation: suggestion.SourceConversation,
		SourceRun:          suggestion.SourceRun,
		Payload:            string(suggestion.Payload),
	}
	// A payload this version cannot decode is not worth dropping the
	// row for: the page still shows the raw JSON and discards it.
	var candidate review.Candidate
	if len(suggestion.Payload) > 0 &&
		json.Unmarshal(suggestion.Payload, &candidate) == nil {
		out.Text = candidate.Text
		out.Scope = candidate.Scope
		out.CandidateKind = candidate.Kind
		if out.Reason == "" {
			out.Reason = candidate.Reason
		}
	}
	return out
}

// ReviewSettings returns the effective review settings plus the queue
// depth per status. Without a user database the settings still load and
// the counts stay zero.
func (b *Review) ReviewSettings() (ReviewState, error) {
	loaded, err := config.LoadReview(b.core.UserDir)
	if err != nil {
		return ReviewState{}, err
	}
	effective, err := loaded.Resolve()
	if err != nil {
		return ReviewState{}, err
	}
	out := ReviewState{
		Enabled:             effective.Enabled,
		EveryTurns:          effective.EveryTurns,
		MinToolCalls:        effective.MinToolCalls,
		OnFailure:           effective.OnFailure,
		MaxSuggestions:      effective.MaxSuggestions,
		TimeoutSeconds:      effective.TimeoutSeconds,
		MinEveryTurns:       config.ReviewMinEveryTurns,
		MaxEveryTurns:       config.ReviewMaxEveryTurns,
		DefaultEveryTurns:   config.ReviewDefaultEveryTurns,
		MinMinToolCalls:     config.ReviewMinMinToolCalls,
		MaxMinToolCalls:     config.ReviewMaxMinToolCalls,
		DefaultMinToolCalls: config.ReviewDefaultMinToolCalls,
		MinMaxSuggestions:   config.ReviewMinMaxSuggestions,
		MaxMaxSuggestions:   config.ReviewMaxMaxSuggestions,
		MinTimeoutSeconds:   config.ReviewMinTimeoutSeconds,
		MaxTimeoutSeconds:   config.ReviewMaxTimeoutSeconds,
	}
	queue, ok := b.reviewQueue()
	if !ok {
		return out, nil
	}
	out.Available = true
	counts := []struct {
		status string
		into   *int
	}{
		{reviewstore.StatusPending, &out.Pending},
		{reviewstore.StatusAccepted, &out.Accepted},
		{reviewstore.StatusDiscarded, &out.Discarded},
	}
	for _, count := range counts {
		n, err := queue.Count(b.core.Shell.Context(), count.status)
		if err != nil {
			return ReviewState{}, err
		}
		*count.into = n
	}
	return out, nil
}

// SaveReviewSettings persists the editable settings and reloads the
// document so the observe hook picks the new cadence up without an app
// restart.
func (b *Review) SaveReviewSettings(req ReviewSettingsRequest) error {
	enabled := req.Enabled
	settings := config.ReviewSettings{
		Enabled:      &enabled,
		EveryTurns:   req.EveryTurns,
		MinToolCalls: req.MinToolCalls,
	}
	if err := config.SaveReview(b.core.UserDir, settings); err != nil {
		return err
	}
	return b.core.ApplyDocumentReload(host.WithAssemblyReason(
		b.core.Shell.Context(), host.ReasonSettingsSave))
}

// ReviewSuggestions lists the pending suggestions, newest first. The
// decided rows stay in the queue as a record but are not what the page
// asks for here.
func (b *Review) ReviewSuggestions() ([]ReviewSuggestionView, error) {
	out := make([]ReviewSuggestionView, 0)
	queue, ok := b.reviewQueue()
	if !ok {
		return out, nil
	}
	rows, err := queue.List(
		b.core.Shell.Context(), reviewstore.StatusPending, 0)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, reviewSuggestionView(row))
	}
	return out, nil
}

// AcceptReviewSuggestion writes the proposed fact through the store's
// single accepted-write path and then marks the row accepted. The
// memory write comes first: a suggestion whose fact the store refuses
// (duplicate, over the limits) stays pending instead of being recorded
// as applied.
func (b *Review) AcceptReviewSuggestion(id string) (ReviewAcceptResult, error) {
	ctx := b.core.Shell.Context()
	queue, err := b.requireReviewQueue()
	if err != nil {
		return ReviewAcceptResult{}, err
	}
	memory, err := requireMemoryStoreOf(b.core)
	if err != nil {
		return ReviewAcceptResult{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ReviewAcceptResult{}, errdefs.Validationf(
			"review: a suggestion id is required")
	}
	suggestion, err := queue.Get(ctx, id)
	if err != nil {
		return ReviewAcceptResult{}, err
	}
	if suggestion.Status != reviewstore.StatusPending {
		return ReviewAcceptResult{}, errdefs.Validationf(
			"review: suggestion %s is already %s", id, suggestion.Status)
	}
	// The queue recorded the workspace the turn ran in; a workspace
	// -scoped fact belongs there, not to whatever the window shows now.
	workspace := strings.TrimSpace(suggestion.SourceWorkspace)
	if workspace == "" {
		workspace = b.core.ActiveWorkDir()
	}
	fact, err := review.ApplyMemory(
		ctx, memory, workspace, suggestion.Payload)
	if err != nil {
		return ReviewAcceptResult{}, fmt.Errorf(
			"review: accept %s: %w", id, err)
	}
	updated, err := queue.SetStatus(ctx, id, reviewstore.StatusAccepted)
	if err != nil {
		return ReviewAcceptResult{}, err
	}
	return ReviewAcceptResult{
		Suggestion: reviewSuggestionView(updated),
		Fact:       memoryFactView(fact),
	}, nil
}

// DiscardReviewSuggestion marks a suggestion discarded. Nothing is
// written to memory: the row is kept so the same candidate is not
// proposed again.
func (b *Review) DiscardReviewSuggestion(id string) (ReviewSuggestionView, error) {
	queue, err := b.requireReviewQueue()
	if err != nil {
		return ReviewSuggestionView{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ReviewSuggestionView{}, errdefs.Validationf(
			"review: a suggestion id is required")
	}
	updated, err := queue.SetStatus(
		b.core.Shell.Context(), id, reviewstore.StatusDiscarded)
	if err != nil {
		return ReviewSuggestionView{}, err
	}
	return reviewSuggestionView(updated), nil
}
