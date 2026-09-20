package config

// Seed helpers for this package's own tests. The exported seeding entry
// points (WriteInference / WriteInferenceOwned / RemoveInferenceConfig)
// were only ever called from tests, so they live here now and wrap the
// production transactional writer.

func seedInference(dir string, cfg InferenceConfig) error {
	return seedInferenceOwned(dir, cfg, nil)
}

func seedInferenceOwned(
	dir string,
	cfg InferenceConfig,
	owners map[string]string,
) error {
	_, err := UpdateInferenceState(dir, func(
		InferenceConfig,
		map[string]string,
	) (InferenceConfig, map[string]string, bool, error) {
		return cfg, owners, true, nil
	})
	if err != nil || owners != nil {
		return err
	}
	// The writer adopts legacy ownership against the config that was
	// already on disk; a fixture seeding the pre-sidecar shape needs one
	// more pass for its own rows to be adopted and persisted.
	_, err = UpdateInferenceState(dir, func(
		next InferenceConfig,
		adopted map[string]string,
	) (InferenceConfig, map[string]string, bool, error) {
		return next, adopted, true, nil
	})
	return err
}

func removeInferenceConfig(dir string) error {
	_, err := UpdateInferenceState(dir, func(
		InferenceConfig,
		map[string]string,
	) (InferenceConfig, map[string]string, bool, error) {
		return InferenceConfig{}, nil, true, nil
	})
	return err
}
