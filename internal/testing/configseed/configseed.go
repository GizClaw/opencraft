// Package configseed seeds user-layer inference configuration for tests.
//
// Seeding goes through the production transactional writer
// (config.UpdateInferenceState), so fixtures exercise the same merge,
// ownership and prune rules the settings page does. These helpers are
// test support: production code writes inference configuration through
// UpdateInferenceState directly.
package configseed

import "github.com/GizClaw/opencraft/internal/foundation/config"

// Write replaces the user-layer inference configuration, reconciling the
// plugin ownership sidecar against the new rows.
func Write(dir string, cfg config.InferenceConfig) error {
	return WriteOwned(dir, cfg, nil)
}

// WriteOwned replaces the configuration and the ownership sidecar.
// A nil owners map preserves the current sidecar — and then persists the
// ownership the write itself implies: the production writer adopts legacy
// rows against the config that was already on disk, so a fixture seeding
// the pre-sidecar shape needs a second pass for its own rows to be
// adopted (the behaviour the deleted WriteInferenceOwned collapsed into
// one call).
func WriteOwned(
	dir string,
	cfg config.InferenceConfig,
	owners map[string]string,
) error {
	_, err := config.UpdateInferenceState(dir, func(
		config.InferenceConfig,
		map[string]string,
	) (config.InferenceConfig, map[string]string, bool, error) {
		return cfg, owners, true, nil
	})
	if err != nil || owners != nil {
		return err
	}
	_, err = config.UpdateInferenceState(dir, func(
		next config.InferenceConfig,
		adopted map[string]string,
	) (config.InferenceConfig, map[string]string, bool, error) {
		return next, adopted, true, nil
	})
	return err
}
