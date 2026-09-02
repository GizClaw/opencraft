package toolchain

import (
	"encoding/json"
	"fmt"
	"os"
)

// loadManifest reads and validates runtimes/manifest.json. The
// manifest carries metadata only (versions, URLs, checksums); binary
// archives are staged by the release pipeline.
func loadManifest(path string) (*manifestFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("runtimes: read manifest %s: %w", path, err)
	}
	var m manifestFile
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("runtimes: parse manifest %s: %w", path, err)
	}
	if m.SchemaVersion == 0 {
		m.SchemaVersion = 1
	}
	if m.SchemaVersion != 1 {
		return nil, fmt.Errorf(
			"runtimes: manifest %s has unsupported schema_version %d",
			path, m.SchemaVersion)
	}
	for family, entry := range map[string]*manifestEntry{
		"python": m.Python,
		"go":     m.Go,
		"node":   m.Node,
		"uv":     m.UV,
	} {
		if entry == nil {
			continue
		}
		if entry.Version == "" {
			return nil, fmt.Errorf(
				"runtimes: manifest %s: %s.version is required", path, family)
		}
		if len(entry.URLs) == 0 || len(entry.SHA256) == 0 {
			return nil, fmt.Errorf(
				"runtimes: manifest %s: %s.urls and %s.sha256 are required",
				path, family, family)
		}
		if len(entry.URLs) != len(entry.SHA256) {
			return nil, fmt.Errorf(
				"runtimes: manifest %s: %s.urls and %s.sha256 must cover the same platforms",
				path, family, family)
		}
		for key := range entry.URLs {
			if _, ok := entry.SHA256[key]; !ok {
				return nil, fmt.Errorf(
					"runtimes: manifest %s: %s.sha256 is missing platform %q",
					path, family, key)
			}
		}
	}
	return &m, nil
}
