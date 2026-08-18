package config

import (
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// DefaultModel returns the first target of the router policy's default
// tier as "provider/name", or "" when the user configuration layer does
// not declare one. It powers the TUI header until the first usage
// report arrives. configDir is the user configuration directory; the
// router policy lives inline in opencraft.yaml (written by first-run
// setup).
func DefaultModel(configDir string) string {
	if configDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		return ""
	}
	return modelFromRouter(data)
}

// modelFromRouter extracts the first default-tier generate target from
// the router resource subtree (resources.router.settings.generate).
func modelFromRouter(data []byte) string {
	var router struct {
		Resources map[string]struct {
			Settings struct {
				Generate []pool `json:"generate"`
			} `json:"settings"`
		} `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &router); err != nil {
		return ""
	}
	res, ok := router.Resources["router"]
	if !ok {
		return ""
	}
	for _, pool := range res.Settings.Generate {
		if len(pool.Targets) == 0 {
			continue
		}
		id := pool.Targets[0].Model.ID
		if id.Provider != "" && id.Name != "" {
			return id.Provider + "/" + id.Name
		}
	}
	return ""
}

type pool struct {
	Targets []struct {
		Model struct {
			ID struct {
				Provider string `json:"provider"`
				Name     string `json:"name"`
			} `json:"id"`
		} `json:"model"`
	} `json:"targets"`
}
