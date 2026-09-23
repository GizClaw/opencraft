package config

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// DelegationSettings is the user-facing shape of the delegation policy:
// how much delegated work may run at once, how deep delegation may
// nest, and which targets may be used at all.
//
// The service limits (concurrency, depth) belong to the delegate
// resource; the target lists belong to the delegate.policy resource.
// Both live in this one shape because one settings card owns them, and
// the user layer keeps them apart (see Load/Save).
//
// The limits are pointers-free on purpose: the settings page always
// submits the complete set, so a written layer carries every key and a
// zero value is never mistaken for "unset". Zero means "use the
// embedded default" on load, which is why the defaults below mirror
// the deploy document.
type DelegationSettings struct {
	MaxConcurrency int      `json:"max_concurrency"`
	MaxDepth       int      `json:"max_depth"`
	AllowedTargets []string `json:"allowed_targets,omitempty"`
	BlockedTargets []string `json:"blocked_targets,omitempty"`
}

// DelegationPolicySettings is the settings shape of the
// delegate.policy resource alone: the target lists, without the
// service limits. It is the one definition the deploy document, the
// runtime resource and the settings page share.
type DelegationPolicySettings struct {
	AllowedTargets []string `json:"allowed_targets,omitempty"`
	BlockedTargets []string `json:"blocked_targets,omitempty"`
}

const (
	// DelegationMaxConcurrencyCeiling and DelegationMaxDepthCeiling
	// bound what one workspace may configure. They exist so a typo
	// cannot turn the runtime into an unbounded fan-out: the delegation
	// service accepts any positive number, and each concurrent
	// delegation is a full agent turn against the same providers.
	DelegationMaxConcurrencyCeiling = 16
	DelegationMaxDepthCeiling       = 16
	// MaxDelegationTargets bounds the allow/block lists. A policy is
	// meant to name the few targets an installation curates, not to
	// mirror a whole registry.
	MaxDelegationTargets = 64
)

// DefaultDelegationSettings mirrors the embedded deploy document.
func DefaultDelegationSettings() DelegationSettings {
	return DelegationSettings{MaxConcurrency: 4, MaxDepth: 8}
}

// Resolve validates the settings and fills the defaults, so callers can
// store what Resolve returns and be sure every field is meaningful.
func (s DelegationSettings) Resolve() (DelegationSettings, error) {
	out := s
	if out.MaxConcurrency == 0 {
		out.MaxConcurrency = DefaultDelegationSettings().MaxConcurrency
	}
	if out.MaxDepth == 0 {
		out.MaxDepth = DefaultDelegationSettings().MaxDepth
	}
	if out.MaxConcurrency < 0 || out.MaxConcurrency > DelegationMaxConcurrencyCeiling {
		return DelegationSettings{}, fmt.Errorf(
			"config: delegation max_concurrency must be between 1 and %d",
			DelegationMaxConcurrencyCeiling)
	}
	if out.MaxDepth < 0 || out.MaxDepth > DelegationMaxDepthCeiling {
		return DelegationSettings{}, fmt.Errorf(
			"config: delegation max_depth must be between 1 and %d",
			DelegationMaxDepthCeiling)
	}
	allowed, err := normalizeTargets(out.AllowedTargets)
	if err != nil {
		return DelegationSettings{}, fmt.Errorf("config: allowed targets: %w", err)
	}
	blocked, err := normalizeTargets(out.BlockedTargets)
	if err != nil {
		return DelegationSettings{}, fmt.Errorf("config: blocked targets: %w", err)
	}
	if err := validatePatternLists(allowed, blocked); err != nil {
		return DelegationSettings{}, err
	}
	out.AllowedTargets = allowed
	out.BlockedTargets = blocked
	return out, nil
}

// NormalizeTargetPatterns trims and de-dupes a target pattern list,
// dropping blanks. It is the write-side normalizer shared by the
// settings page and the runtime policy.
//
// A list over the bound is truncated rather than dropped: this is also
// the runtime's last step before the matcher, and emptying an over-long
// allowlist there would widen access instead of narrowing it.
func NormalizeTargetPatterns(names []string) []string {
	if len(names) > MaxDelegationTargets {
		names = names[:MaxDelegationTargets]
	}
	out, err := normalizeTargets(names)
	if err != nil {
		// normalizeTargets only fails on the list bound, which the
		// truncation above has already handled.
		return nil
	}
	return out
}

// ValidateTargetPatterns checks a policy pair: every pattern must be
// usable by the matcher, and a target cannot be both allowed and
// blocked (the contradiction would silently resolve to "blocked").
func ValidateTargetPatterns(allowed, blocked []string) error {
	normalizedAllowed, err := normalizeTargets(allowed)
	if err != nil {
		return err
	}
	normalizedBlocked, err := normalizeTargets(blocked)
	if err != nil {
		return err
	}
	return validatePatternLists(normalizedAllowed, normalizedBlocked)
}

// LoadDelegation reads the effective delegation settings: the embedded
// defaults, the deploy document's service limits, then the user layer.
func LoadDelegation(configDir string) (DelegationSettings, error) {
	settings := DefaultDelegationSettings()
	if base, err := EmbeddedOpenCraft(); err == nil {
		if embedded, err := decodeDelegationSettings(base); err == nil {
			settings = overlayDelegationSettings(settings, embedded)
		}
	}
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return settings, nil
		}
		return DelegationSettings{}, err
	}
	user, err := decodeDelegationSettings(data)
	if err != nil {
		return DelegationSettings{}, fmt.Errorf(
			"config: parse delegation settings: %w", err)
	}
	return overlayDelegationSettings(settings, user), nil
}

// SaveDelegation validates the settings and writes them into the user
// layer: the service limits are deep-merged into the existing delegate
// resource (its deps and any hand-added key survive), and the target
// lists replace the delegate.policy resource wholesale (the policy is
// generator-owned, so a removed target cannot linger). A cleared list
// is written as an explicit `[]`, never as an empty settings object:
// deploy reads the latter as an empty resource source and refuses the
// whole document, and "no restriction" is what the empty list says.
func SaveDelegation(configDir string, settings DelegationSettings) error {
	resolved, err := settings.Resolve()
	if err != nil {
		return err
	}
	layer := delegationLayer{Version: "v1"}
	layer.Resources.Delegate = &delegationServiceLayer{
		Settings: delegationServiceSettings{
			MaxConcurrency: resolved.MaxConcurrency,
			MaxDepth:       resolved.MaxDepth,
		},
	}
	layer.Resources.DelegatePolicy = &delegationPolicyLayer{
		Kind: "opencraft.delegation.policy",
		Impl: "local",
		Settings: delegationPolicySettings{
			AllowedTargets: resolved.AllowedTargets,
			BlockedTargets: resolved.BlockedTargets,
		},
	}
	fresh, err := yaml.Marshal(layer)
	if err != nil {
		return fmt.Errorf("config: render delegation layer: %w", err)
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		map[string]bool{"delegate.policy": true},
		map[string]bool{"delegate": true},
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

type delegationLayer struct {
	Version   string `json:"version"`
	Resources struct {
		Delegate       *delegationServiceLayer `json:"delegate,omitempty"`
		DelegatePolicy *delegationPolicyLayer  `json:"delegate.policy,omitempty"`
	} `json:"resources"`
}

type delegationServiceLayer struct {
	Settings delegationServiceSettings `json:"settings"`
}

type delegationServiceSettings struct {
	MaxConcurrency int `json:"max_concurrency"`
	MaxDepth       int `json:"max_depth"`
}

type delegationPolicyLayer struct {
	Kind     string                   `json:"kind,omitempty"`
	Impl     string                   `json:"impl,omitempty"`
	Settings delegationPolicySettings `json:"settings"`
}

// delegationPolicySettings is the policy subtree as written into the
// user layer. Both lists are always emitted, empty ones as `[]`: the
// card submits the complete set, and an empty list is a value ("no
// restriction"), not an absent key — the settings object must never
// marshal to `{}`, which deploy rejects as an empty resource source.
type delegationPolicySettings struct {
	AllowedTargets []string `json:"allowed_targets"`
	BlockedTargets []string `json:"blocked_targets"`
}

func decodeDelegationSettings(data []byte) (DelegationSettings, error) {
	var doc struct {
		Resources map[string]struct {
			Settings struct {
				MaxConcurrency int      `json:"max_concurrency"`
				MaxDepth       int      `json:"max_depth"`
				AllowedTargets []string `json:"allowed_targets"`
				BlockedTargets []string `json:"blocked_targets"`
			} `json:"settings"`
		} `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return DelegationSettings{}, err
	}
	service := doc.Resources["delegate"].Settings
	policy := doc.Resources["delegate.policy"].Settings
	return DelegationSettings{
		MaxConcurrency: service.MaxConcurrency,
		MaxDepth:       service.MaxDepth,
		AllowedTargets: policy.AllowedTargets,
		BlockedTargets: policy.BlockedTargets,
	}, nil
}

// overlayDelegationSettings applies the overlay's non-zero values.
// Target lists replace the layer below's — SaveDelegation rewrites the
// policy resource wholesale, so saving empty lists clears a restriction
// the user set earlier — but an empty list never clears one carried by
// a lower layer: like every other zero value here, it means "keep what
// the layer below says".
func overlayDelegationSettings(base, overlay DelegationSettings) DelegationSettings {
	if overlay.MaxConcurrency > 0 {
		base.MaxConcurrency = overlay.MaxConcurrency
	}
	if overlay.MaxDepth > 0 {
		base.MaxDepth = overlay.MaxDepth
	}
	if len(overlay.AllowedTargets) > 0 {
		base.AllowedTargets = overlay.AllowedTargets
	}
	if len(overlay.BlockedTargets) > 0 {
		base.BlockedTargets = overlay.BlockedTargets
	}
	return base
}

func normalizeTargets(names []string) ([]string, error) {
	if len(names) > MaxDelegationTargets {
		return nil, fmt.Errorf("at most %d targets are supported", MaxDelegationTargets)
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if strings.ContainsAny(trimmed, " \t/\\") {
			return nil, fmt.Errorf(
				"%q is not a target name (no whitespace or separators)", trimmed)
		}
		if containsTarget(out, trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return out, nil
}

func containsTarget(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

// validatePatternLists rejects unusable patterns and list pairs that
// contradict themselves. Every pattern must compile: a policy is only
// worth having if what it says is what it does.
func validatePatternLists(allowed, blocked []string) error {
	for _, list := range [][]string{allowed, blocked} {
		for _, pattern := range list {
			if _, err := path.Match(pattern, "probe"); err != nil {
				return fmt.Errorf(
					"config: target pattern %q is malformed: %w", pattern, err)
			}
		}
	}
	for _, name := range allowed {
		if containsTarget(blocked, name) {
			return fmt.Errorf(
				"config: target %q is both allowed and blocked", name)
		}
	}
	return nil
}
