package config

import (
	"testing"

	"sigs.k8s.io/yaml"
)

func FuzzUserConfigYAML(f *testing.F) {
	for _, seed := range []string{
		"version: v1\nresources: {}\n",
		"resources:\n  provider.openai:\n    settings: {}\n",
		"",
		"garbage: [",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		var doc struct {
			Resources map[string]jsonRaw `json:"resources"`
		}
		_ = yaml.Unmarshal([]byte(input), &doc)
	})
}

type jsonRaw []byte
