// Package config owns opencraft's embedded deploy assets: the internal
// base opencraft.yaml document, the seeded user layer, the execution
// config, the graph definition, and prompt templates.
package config

import "embed"

// Default deploy assets. The base opencraft.yaml and graph definition
// are internal wiring and stay embedded; the user layer and
// execution.yaml are seeded into ~/.opencraft/config/.
//
//go:embed assets/opencraft.yaml assets/user_opencraft.yaml assets/inference.yaml assets/graphs/assistant.yaml assets/graphs/node/world.js assets/prompts/system.md
var assets embed.FS

// UserAssets are the embedded files seeded into ~/.opencraft/config/
// (embed ref -> on-disk name).
var UserAssets = []struct{ Ref, Name string }{
	{Ref: "assets/user_opencraft.yaml", Name: "opencraft.yaml"},
	{Ref: "assets/inference.yaml", Name: "inference.yaml"},
}

// FS returns the embedded deploy asset filesystem (for embed sources
// referenced from opencraft.yaml, e.g. the graph definition).
func FS() embed.FS { return assets }

// EmbeddedOpenCraft returns the internal deploy document.
func EmbeddedOpenCraft() ([]byte, error) {
	return assets.ReadFile("assets/opencraft.yaml")
}
