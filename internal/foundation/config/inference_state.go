package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/GizClaw/opencraft/internal/foundation/compat"
)

// The config-state lock and the plugin ownership sidecar. The rows
// written into opencraft.yaml and the map of which plugin owns each of
// them form one logical state, so every writer and the plugin lifecycle
// synchronize on inferenceStateMu.

// providerOwnersFileName records, outside the flowcraft deployment
// document, which installed plugin owns each plugin-submitted
// inference instance. Flowcraft's strict provider settings cannot
// carry an extra owner field, so the stable provider profile id cannot
// be the sole ownership carrier once a plugin may submit several
// instances.
const providerOwnersFileName = "plugin-provider-owners.json"

// adoptLegacyProviderOwners records ownership for inference rows
// written under the single-instance plugin contract that predates the
// ownership sidecar. In that scheme the host only accepted instance
// ids equal to the calling plugin id and the key reference lived
// inside the plugin's secret namespace, so a row matching both shapes
// could only have been created by the plugin with that id. Rows like
// this carry no sidecar record after an upgrade; without adoption the
// plugin can neither replace nor remove its own stale deployment,
// leaving duplicate inference entries behind.
func adoptLegacyProviderOwners(cfg InferenceConfig, owners map[string]string) {
	for _, in := range cfg.Instances {
		if _, ok := owners[in.StableID]; ok {
			continue
		}
		if in.KeySource == KeyKeychain &&
			compat.LegacyPluginKeyRef(in.StableID, in.KeyValue) {
			owners[in.StableID] = in.StableID
		}
	}
}

// inferenceStateMu serializes inference config + owner writes. Both
// files together form one logical state (rows and their plugin owners);
// a plugin upsert, settings save and plugin disable/uninstall can run
// concurrently.
var inferenceStateMu sync.Mutex

// providerOwnersPath returns the sidecar path inside the user config
// directory.
func providerOwnersPath(configDir string) string {
	return filepath.Join(configDir, providerOwnersFileName)
}

// LoadProviderOwners returns the current plugin→instance ownership,
// including legacy rows created under the single-instance plugin
// contract (see adoptLegacyProviderOwners). A missing sidecar file is
// an empty map, not an error.
func LoadProviderOwners(configDir string) (map[string]string, error) {
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	owners, err := loadProviderOwnersLocked(configDir)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadInference(configDir)
	if err != nil {
		return nil, err
	}
	adoptLegacyProviderOwners(cfg, owners)
	return owners, nil
}

func loadProviderOwnersLocked(configDir string) (map[string]string, error) {
	data, err := os.ReadFile(providerOwnersPath(configDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", providerOwnersFileName, err)
	}
	if len(data) == 0 {
		return map[string]string{}, nil
	}
	owners := map[string]string{}
	if err := json.Unmarshal(data, &owners); err != nil {
		return nil, fmt.Errorf(
			"config: decode %s: %w", providerOwnersFileName, err)
	}
	return owners, nil
}

// DropProviderOwners removes every ownership row whose owning plugin
// id matches pluginID and reports how many were dropped. It is the
// owner-sidecar update used when a plugin is disabled or uninstalled
// without an inference config rewrite.
func DropProviderOwners(configDir, pluginID string) (int, error) {
	inferenceStateMu.Lock()
	defer inferenceStateMu.Unlock()
	owners, err := loadProviderOwnersLocked(configDir)
	if err != nil {
		return 0, err
	}
	dropped := 0
	for id, owner := range owners {
		if owner == pluginID {
			delete(owners, id)
			dropped++
		}
	}
	if dropped == 0 {
		return 0, nil
	}
	return dropped, saveProviderOwnersLocked(configDir, owners)
}

func saveProviderOwnersLocked(configDir string, owners map[string]string) error {
	if len(owners) == 0 {
		path := providerOwnersPath(configDir)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("config: remove %s: %w", providerOwnersFileName, err)
		}
		return nil
	}
	data, err := json.MarshalIndent(owners, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode %s: %w", providerOwnersFileName, err)
	}
	data = append(data, '\n')
	return writeFileAtomic(providerOwnersPath(configDir), data, 0o600)
}

// reconcileProviderOwners keeps only owner rows whose stable id still
// exists in cfg, so a settings save that drops a plugin row also drops
// its stale ownership record.
func reconcileProviderOwners(
	owners map[string]string,
	instances []Instance,
) map[string]string {
	alive := make(map[string]bool, len(instances))
	for _, in := range instances {
		if in.StableID != "" {
			alive[in.StableID] = true
		}
	}
	out := make(map[string]string, len(owners))
	for id, owner := range owners {
		if owner != "" && alive[id] {
			out[id] = owner
		}
	}
	return out
}

// inferenceOwnedBy reports whether stableID is owned by pluginID. The
// explicit sidecar is the only ownership source; instance ids are not
// tied to plugin ids.
func inferenceOwnedBy(
	owners map[string]string,
	stableID string,
	pluginID string,
) bool {
	owner, ok := owners[stableID]
	return ok && owner == pluginID
}
