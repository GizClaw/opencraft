// Package config owns opencraft's embedded deploy assets: the internal
// opencraft.yaml document, the user-seeded inference/workspace configs,
// the graph definition, and prompt templates.
package config

import "embed"

// Default deploy assets. opencraft.yaml and the graph definition are
// internal wiring and stay embedded; inference.yaml and workspace.yaml
// are seeded into ~/.opencraft/config/ for user configuration.
//
//go:embed assets/opencraft.yaml assets/inference.yaml assets/workspace.yaml assets/tools.yaml assets/execution.yaml assets/graphs/assistant.yaml assets/graphs/node/world.js assets/prompts/system.md
var assets embed.FS

// UserAssets are the embedded files seeded into ~/.opencraft/config/.
var UserAssets = []string{
	"inference.yaml",
	"workspace.yaml",
	"tools.yaml",
	"execution.yaml",
}

// SandboxYAML returns the platform-specific embedded sandbox document
// (seatbelt on macOS, bwrap on Linux, local elsewhere), selected at
// compile time via build tags, with ${CACHE_DIR} expanded to cacheDir.
func SandboxYAML(cacheDir string) []byte {
	return platformSandbox(cacheDir)
}

// FS returns the embedded deploy asset filesystem (for embed sources
// referenced from opencraft.yaml, e.g. the graph definition).
func FS() embed.FS { return assets }

// EmbeddedOpenCraft returns the internal deploy document.
func EmbeddedOpenCraft() ([]byte, error) {
	return assets.ReadFile("assets/opencraft.yaml")
}
