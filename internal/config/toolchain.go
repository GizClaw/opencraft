package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/GizClaw/opencraft/internal/toolchain"
)

// ToolchainSettings is the user-facing slice of the toolchain resource
// the settings page manages. Only preference is user-editable; root /
// manifest paths are release-owned.
type ToolchainSettings struct {
	Preference string `json:"preference,omitempty"`
}

// LoadToolchainPreference returns the effective runtime_preference
// from the user configuration layer, falling back to external-first.
func LoadToolchainPreference(configDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return string(toolchain.PreferenceExternalFirst), nil
		}
		return "", err
	}
	var doc struct {
		Resources map[string]struct {
			Settings ToolchainSettings `json:"settings"`
		} `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("config: parse toolchain settings: %w", err)
	}
	pref := doc.Resources["toolchain"].Settings.Preference
	if pref == "" {
		return string(toolchain.PreferenceExternalFirst), nil
	}
	if _, err := toolchain.NormalizePreference(pref); err != nil {
		return "", err
	}
	return pref, nil
}

// WriteToolchainPreference persists runtime_preference into the user
// configuration layer, deep-merging so hand-written sibling keys
// survive.
func WriteToolchainPreference(configDir, preference string) error {
	normalized, err := toolchain.NormalizePreference(preference)
	if err != nil {
		return err
	}
	layer := toolchainLayer{Version: "v1"}
	layer.Resources.Toolchain = &toolchainResourceLayer{
		Settings: ToolchainSettings{Preference: string(normalized)},
	}
	fresh, err := yaml.Marshal(layer)
	if err != nil {
		return fmt.Errorf("config: render toolchain layer: %w", err)
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		map[string]bool{},
		map[string]bool{"toolchain": true},
		false, // toolchain does not own provider resources; preserve them
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

type toolchainLayer struct {
	Version   string `json:"version"`
	Resources struct {
		Toolchain *toolchainResourceLayer `json:"toolchain,omitempty"`
	} `json:"resources"`
}

type toolchainResourceLayer struct {
	Settings ToolchainSettings `json:"settings"`
}
