// User-level memory settings.
//
// This file owns the usermemory resource's settings shape, its defaults
// and validation, and the user-layer persistence the settings page
// uses. The deploy document carries the same subtree (assets/
// opencraft.yaml), the worldstate user-memory section and the remember
// tool decode their factory settings with these types, so the page and
// the runtime cannot drift.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// ResourceUserMemory is the deploy-document id of the user-level memory
// resource. External callers that need to reference it (bindings, the
// review UI) use this instead of repeating the string.
const ResourceUserMemory = "usermemory"

// Bounds and defaults for the settings. MaxItems/MaxTextBytes mirror
// the store's hard limits (capabilities/memory/userstore) and are
// reported read-only on the page.
const (
	UserMemoryDefaultInjectMaxItems = 12
	UserMemoryMinInjectMaxItems     = 1
	UserMemoryMaxInjectMaxItems     = 50

	UserMemoryDefaultInjectMaxChars = 2048
	UserMemoryMinInjectMaxChars     = 256
	UserMemoryMaxInjectMaxChars     = 16384
)

// UserMemorySettings is the usermemory resource's settings subtree.
type UserMemorySettings struct {
	// Enabled turns the whole feature off: no section is injected and
	// the remember tool is not contributed.
	Enabled *bool `json:"enabled,omitempty"`
	// InjectMaxItems caps how many facts ride into one turn.
	InjectMaxItems int `json:"inject_max_items,omitempty"`
	// InjectMaxChars caps the injected section's byte size; the newest
	// facts win when the budget runs out.
	InjectMaxChars int `json:"inject_max_chars,omitempty"`
}

// UserMemoryConfig is the validated, defaulted view the runtime and the
// settings page use.
type UserMemoryConfig struct {
	Enabled        bool
	InjectMaxItems int
	InjectMaxChars int
}

// DefaultUserMemorySettings returns the shipped settings.
func DefaultUserMemorySettings() UserMemorySettings {
	enabled := true
	return UserMemorySettings{
		Enabled:        &enabled,
		InjectMaxItems: UserMemoryDefaultInjectMaxItems,
		InjectMaxChars: UserMemoryDefaultInjectMaxChars,
	}
}

// Resolve validates the settings and applies defaults.
func (s UserMemorySettings) Resolve() (UserMemoryConfig, error) {
	out := UserMemoryConfig{
		Enabled:        s.Enabled == nil || *s.Enabled,
		InjectMaxItems: s.InjectMaxItems,
		InjectMaxChars: s.InjectMaxChars,
	}
	if out.InjectMaxItems == 0 {
		out.InjectMaxItems = UserMemoryDefaultInjectMaxItems
	}
	if out.InjectMaxItems < UserMemoryMinInjectMaxItems ||
		out.InjectMaxItems > UserMemoryMaxInjectMaxItems {
		return UserMemoryConfig{}, fmt.Errorf(
			"user memory: inject_max_items %d out of range (%d-%d)",
			out.InjectMaxItems,
			UserMemoryMinInjectMaxItems, UserMemoryMaxInjectMaxItems)
	}
	if out.InjectMaxChars == 0 {
		out.InjectMaxChars = UserMemoryDefaultInjectMaxChars
	}
	if out.InjectMaxChars < UserMemoryMinInjectMaxChars ||
		out.InjectMaxChars > UserMemoryMaxInjectMaxChars {
		return UserMemoryConfig{}, fmt.Errorf(
			"user memory: inject_max_chars %d out of range (%d-%d)",
			out.InjectMaxChars,
			UserMemoryMinInjectMaxChars, UserMemoryMaxInjectMaxChars)
	}
	return out, nil
}

// readUserLayerBytes reads the user configuration layer, returning nil
// when the file does not exist yet.
func readUserLayerBytes(configDir string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// userMemoryLayer is the user-layer document SaveUserMemory writes.
type userMemoryLayer struct {
	Version   string `json:"version"`
	Resources struct {
		UserMemory *userMemoryResourceLayer `json:"usermemory,omitempty"`
	} `json:"resources"`
}

type userMemoryResourceLayer struct {
	Settings UserMemorySettings `json:"settings"`
}

// LoadUserMemory returns the effective settings: embedded defaults
// overlaid with the user layer's resources.usermemory.settings.
func LoadUserMemory(configDir string) (UserMemorySettings, error) {
	settings, err := layeredResourceSettings[UserMemorySettings](
		configDir, ResourceUserMemory)
	if err != nil {
		return UserMemorySettings{}, err
	}
	return settings, nil
}

// SaveUserMemory validates the settings and persists them as the user
// layer's usermemory resource. The resource is replaced wholesale, so
// disabling the feature or lowering a budget takes effect; unrelated
// user-layer resources survive.
func SaveUserMemory(configDir string, settings UserMemorySettings) error {
	if _, err := settings.Resolve(); err != nil {
		return err
	}
	layer := userMemoryLayer{Version: "v1"}
	layer.Resources.UserMemory = &userMemoryResourceLayer{Settings: settings}
	fresh, err := yaml.Marshal(layer)
	if err != nil {
		return fmt.Errorf("config: render user memory layer: %w", err)
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		map[string]bool{ResourceUserMemory: true},
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

// layeredResourceSettings returns the effective settings of one
// resource: the embedded layer's subtree overlaid key by key with the
// user layer's, decoded into T. A missing resource on both sides yields
// the zero value (callers apply defaults with Resolve).
func layeredResourceSettings[T any](configDir, id string) (T, error) {
	merged := map[string]any{}
	if embedded, err := EmbeddedOpenCraft(); err == nil {
		settings, err := resourceSettingsMap(embedded, id)
		if err != nil {
			return zeroOf[T](), fmt.Errorf(
				"config: parse embedded %s: %w", id, err)
		}
		for key, value := range settings {
			merged[key] = value
		}
	}
	data, err := readUserLayerBytes(configDir)
	if err != nil {
		return zeroOf[T](), err
	}
	if data != nil {
		settings, err := resourceSettingsMap(data, id)
		if err != nil {
			return zeroOf[T](), fmt.Errorf("config: parse %s: %w", id, err)
		}
		for key, value := range settings {
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return zeroOf[T](), nil
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return zeroOf[T](), fmt.Errorf("config: render %s settings: %w", id, err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zeroOf[T](), fmt.Errorf("config: decode %s settings: %w", id, err)
	}
	return out, nil
}

func zeroOf[T any]() T {
	var zero T
	return zero
}

// resourceSettingsMap extracts resources[id].settings as a plain map.
func resourceSettingsMap(data []byte, id string) (map[string]any, error) {
	var doc struct {
		Resources map[string]struct {
			Settings map[string]any `json:"settings"`
		} `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	resource, ok := doc.Resources[strings.TrimSpace(id)]
	if !ok {
		return nil, nil
	}
	return resource.Settings, nil
}
