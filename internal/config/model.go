package config

import "sigs.k8s.io/yaml"

// DefaultModel returns the first target of the router policy's default
// tier as "provider/name", or "" when inference.yaml does not declare
// one. It powers the TUI header until the first usage report arrives.
func DefaultModel() string {
	data, err := FS().ReadFile("assets/inference.yaml")
	if err != nil {
		return ""
	}
	var g struct {
		Generate []struct {
			Targets []struct {
				Model struct {
					ID struct {
						Provider string `json:"provider"`
						Name     string `json:"name"`
					} `json:"id"`
				} `json:"model"`
			} `json:"targets"`
		} `json:"generate"`
	}
	if err := yaml.Unmarshal(data, &g); err != nil {
		return ""
	}
	for _, pool := range g.Generate {
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
