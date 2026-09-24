package bindings

import (
	"sort"
	"strings"

	"github.com/GizClaw/flowcraft/core/delegation"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// Delegation is the settings face of the delegation policy: how much
// delegated work may run at once, how deep it may nest, and which
// targets the model may hand work to.
//
// The limits belong to the delegate service and the lists belong to the
// delegate.policy resource; both are read at assembly, so a save
// reloads the document instead of poking the running service.
type Delegation struct {
	core *core.Core
}

// NewDelegationBinding builds the delegation settings service.
func NewDelegationBinding(c *core.Core) *Delegation {
	return &Delegation{core: c}
}

// DelegationState is the card's whole state: the effective settings,
// the writable bounds, and the targets the runtime currently offers —
// so a policy can be curated from a list instead of from memory.
type DelegationState struct {
	MaxConcurrency int      `json:"max_concurrency"`
	MaxDepth       int      `json:"max_depth"`
	AllowedTargets []string `json:"allowed_targets"`
	BlockedTargets []string `json:"blocked_targets"`

	MinMaxConcurrency     int `json:"min_max_concurrency"`
	MaxMaxConcurrency     int `json:"max_max_concurrency"`
	DefaultMaxConcurrency int `json:"default_max_concurrency"`
	MinMaxDepth           int `json:"min_max_depth"`
	MaxMaxDepth           int `json:"max_max_depth"`
	DefaultMaxDepth       int `json:"default_max_depth"`
	MaxTargets            int `json:"max_targets"`

	Targets []string `json:"targets"`
	// TargetsAvailable reports whether the list came from a live
	// runtime: without one the card still edits the policy, it just
	// has nothing to suggest.
	TargetsAvailable bool `json:"targets_available"`
}

// DelegationSettingsRequest is the save payload of the delegation card.
type DelegationSettingsRequest struct {
	MaxConcurrency int      `json:"max_concurrency"`
	MaxDepth       int      `json:"max_depth"`
	AllowedTargets []string `json:"allowed_targets"`
	BlockedTargets []string `json:"blocked_targets"`
}

// DelegationState returns the effective settings, their bounds, and the
// registered targets of the live runtime.
func (b *Delegation) DelegationState() (DelegationState, error) {
	loaded, err := config.LoadDelegation(b.core.UserDir)
	if err != nil {
		return DelegationState{}, err
	}
	effective, err := loaded.Resolve()
	if err != nil {
		return DelegationState{}, err
	}
	defaults := config.DefaultDelegationSettings()
	state := DelegationState{
		MaxConcurrency:        effective.MaxConcurrency,
		MaxDepth:              effective.MaxDepth,
		MinMaxConcurrency:     1,
		MaxMaxConcurrency:     config.DelegationMaxConcurrencyCeiling,
		DefaultMaxConcurrency: defaults.MaxConcurrency,
		MinMaxDepth:           1,
		MaxMaxDepth:           config.DelegationMaxDepthCeiling,
		DefaultMaxDepth:       defaults.MaxDepth,
		MaxTargets:            config.MaxDelegationTargets,
	}
	// The lists are never null in the payload: the card renders them as
	// editable rows, and JSON null would make it special-case absence.
	state.AllowedTargets = append([]string{}, effective.AllowedTargets...)
	state.BlockedTargets = append([]string{}, effective.BlockedTargets...)
	state.Targets, state.TargetsAvailable = b.delegationTargets()
	if state.Targets == nil {
		state.Targets = []string{}
	}
	return state, nil
}

// delegationTargets reads the registered delegation targets out of the
// live runtime's directory. It is best effort: a runtime that is not
// assembled yet reports no suggestions rather than failing the card.
func (b *Delegation) delegationTargets() ([]string, bool) {
	h := b.core.ActiveHost()
	if h == nil || h.Controller() == nil || h.Controller().Runtime() == nil {
		return nil, false
	}
	value, ok := h.Controller().Runtime().Resource("delegate.directory")
	if !ok {
		return nil, false
	}
	dir, ok := value.(delegation.Directory)
	if !ok || dir == nil {
		return nil, false
	}
	targets, err := dir.List(b.core.Shell.Context())
	if err != nil {
		return nil, false
	}
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		if name := strings.TrimSpace(target.ID); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, true
}

// SaveDelegationSettings persists the limits and the policy into the
// user layer and reloads the document, so the running assembly picks
// them up without an app restart.
func (b *Delegation) SaveDelegationSettings(
	req DelegationSettingsRequest,
) error {
	settings := config.DelegationSettings{
		MaxConcurrency: req.MaxConcurrency,
		MaxDepth:       req.MaxDepth,
		AllowedTargets: req.AllowedTargets,
		BlockedTargets: req.BlockedTargets,
	}
	if err := config.SaveDelegation(b.core.UserDir, settings); err != nil {
		return err
	}
	return b.core.ApplyDocumentReload(host.WithAssemblyReason(
		b.core.Shell.Context(), host.ReasonSettingsSave))
}
