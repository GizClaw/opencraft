package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/deploy"
	"sigs.k8s.io/yaml"
)

// Queries over the user layer and the merged deployment document that
// answer whether inference is wired.

// InferenceNeeded reports whether the user configuration layer carries
// no enabled inference wiring: no file at all, or a router with no
// generate targets. It answers the same question RouterConfigured
// answers on the merged document, from the user layer alone.
func InferenceNeeded(configDir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		// No user layer: definitely unconfigured.
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	var doc struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, fmt.Errorf(
			"config: parse user config: %w", err)
	}
	raw, ok := doc.Resources["router"]
	if !ok {
		return true, nil
	}
	var res struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, fmt.Errorf("config: parse user router: %w", err)
	}
	targeted, err := routerHasTargets(res.Settings)
	if err != nil {
		return false, err
	}
	return !targeted, nil
}

// RouterConfigured reports whether the merged deployment document
// carries at least one router generate target. The generated user
// layer always declares the router, so an empty target list
// distinguishes "inference is not configured yet" (an expected UI
// state) from a real router validation failure at build time.
func RouterConfigured(doc deploy.Document) (bool, error) {
	res, ok := doc.Resources["router"]
	if !ok {
		return false, nil
	}
	return routerHasTargets(res.Settings)
}

// routerHasTargets reports whether one router settings document
// declares at least one generate target.
func routerHasTargets(settings json.RawMessage) (bool, error) {
	if len(settings) == 0 {
		return false, nil
	}
	var policy struct {
		Generate []struct {
			Targets []json.RawMessage `json:"targets"`
		} `json:"generate"`
	}
	if err := json.Unmarshal(settings, &policy); err != nil {
		return false, fmt.Errorf("config: decode router policy: %w", err)
	}
	for _, pool := range policy.Generate {
		if len(pool.Targets) > 0 {
			return true, nil
		}
	}
	return false, nil
}
