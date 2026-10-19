package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// MemorySettings mirrors the resources.mem.settings subtree the
// settings page manages. The settings page always submits the complete
// set, so no omitempty: every write persists all four keys and false is
// never dropped.
type MemorySettings struct {
	MaxRawMessages    int  `json:"max_raw_messages"`
	PreserveRecent    int  `json:"preserve_recent"`
	MaxSummaryBytes   int  `json:"max_summary_bytes"`
	ReplayFullHistory bool `json:"replay_full_history"`
}

// defaultMemorySettings matches the embedded deploy document.
func defaultMemorySettings() MemorySettings {
	return MemorySettings{
		MaxRawMessages:    36,
		PreserveRecent:    4,
		MaxSummaryBytes:   4096,
		ReplayFullHistory: true,
	}
}

// LoadMemory returns the effective memory settings: embedded defaults
// overlaid with the user layer's resources.mem.settings.
func LoadMemory(configDir string) (MemorySettings, error) {
	settings := defaultMemorySettings()
	if base, err := EmbeddedOpenCraft(); err == nil {
		if embedded, err := decodeMemorySettings(base); err == nil {
			settings = overlayMemorySettings(settings, embedded)
		}
	}
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return settings, nil
		}
		return MemorySettings{}, err
	}
	user, err := decodeMemorySettings(data)
	if err != nil {
		return MemorySettings{}, fmt.Errorf("config: parse memory settings: %w", err)
	}
	return overlayMemorySettings(settings, user), nil
}

// WriteMemory persists the memory settings into the user configuration
// layer. The mem resource is replaced wholesale by the fresh partial
// resource, so stale or unknown keys cannot survive in
// resources.mem.settings and later fail the runtime's strict decode.
func WriteMemory(configDir string, settings MemorySettings) error {
	layer := memoryLayer{Version: "v1"}
	layer.Resources.Mem = &memoryResourceLayer{Settings: settings}
	fresh, err := yaml.Marshal(layer)
	if err != nil {
		return fmt.Errorf("config: render memory layer: %w", err)
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		map[string]bool{"mem": true},
		map[string]bool{},
		map[string]bool{},
		false, // memory does not own provider resources; preserve them
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

type memoryLayer struct {
	Version   string `json:"version"`
	Resources struct {
		Mem *memoryResourceLayer `json:"mem,omitempty"`
	} `json:"resources"`
}

type memoryResourceLayer struct {
	Settings MemorySettings `json:"settings"`
}

// memorySettingsDoc is one layer's resources.mem.settings as written.
// ReplayFullHistory is a pointer so a layer that does not mention the
// switch stays distinguishable from one that turns it off: the default
// is on, and a hand-written layer with only max_raw_messages must not
// silently switch it back off by decoding a zero value over it.
type memorySettingsDoc struct {
	MaxRawMessages    int   `json:"max_raw_messages"`
	PreserveRecent    int   `json:"preserve_recent"`
	MaxSummaryBytes   int   `json:"max_summary_bytes"`
	ReplayFullHistory *bool `json:"replay_full_history"`
}

func decodeMemorySettings(data []byte) (memorySettingsDoc, error) {
	var doc struct {
		Resources map[string]struct {
			Settings memorySettingsDoc `json:"settings"`
		} `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return memorySettingsDoc{}, err
	}
	return doc.Resources["mem"].Settings, nil
}

// overlayMemorySettings applies the values a layer declares onto base.
// A number left at zero and a switch left absent both mean "this layer
// does not say", which is why the decoders keep absence visible.
func overlayMemorySettings(base MemorySettings, overlay memorySettingsDoc) MemorySettings {
	if overlay.MaxRawMessages > 0 {
		base.MaxRawMessages = overlay.MaxRawMessages
	}
	if overlay.PreserveRecent > 0 {
		base.PreserveRecent = overlay.PreserveRecent
	}
	if overlay.MaxSummaryBytes > 0 {
		base.MaxSummaryBytes = overlay.MaxSummaryBytes
	}
	if overlay.ReplayFullHistory != nil {
		base.ReplayFullHistory = *overlay.ReplayFullHistory
	}
	return base
}
