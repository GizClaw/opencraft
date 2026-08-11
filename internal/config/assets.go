// Package config owns opencraft's embedded deploy assets: the internal
// opencraft.yaml document, the user-seeded inference/workspace configs,
// the graph definition, and prompt templates.
package config

import "embed"

// Default deploy assets. opencraft.yaml and the graph definition are
// internal wiring and stay embedded; inference.yaml and workspace.yaml
// are seeded into ~/.opencraft/config/ for user configuration.
//
//go:embed opencraft.yaml inference.yaml workspace.yaml tools.yaml graphs/assistant.yaml graphs/node/world.js prompts/system.md
var assets embed.FS

// UserAssets are the embedded files seeded into ~/.opencraft/config/.
var UserAssets = []string{
	"inference.yaml",
	"workspace.yaml",
	"tools.yaml",
}

// FS returns the embedded deploy asset filesystem (for embed sources
// referenced from opencraft.yaml, e.g. the graph definition).
func FS() embed.FS { return assets }

// EmbeddedOpenCraft returns the internal deploy document.
func EmbeddedOpenCraft() ([]byte, error) {
	return assets.ReadFile("opencraft.yaml")
}
