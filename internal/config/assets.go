// Package config owns opencraft's embedded deploy assets: the internal
// base opencraft.yaml document, the seeded user layer, the execution
// config, the graph definition, and prompt templates.
package config

import "embed"

// Default deploy assets. The base opencraft.yaml stays embedded as
// internal wiring; the user layer, inference routing, and the graph
// definition (with its referenced prompt and world script) are seeded
// into ~/.opencraft/config/ so they are editable at runtime.
//
//go:embed assets/opencraft.yaml assets/user_opencraft.yaml assets/inference.yaml assets/graphs/assistant.yaml assets/graphs/node/world.js assets/prompts/system.md
var assets embed.FS

// UserAssets are the embedded files seeded into ~/.opencraft/config/
// (embed ref -> on-disk name).
var UserAssets = []struct{ Ref, Name string }{
	{Ref: "assets/user_opencraft.yaml", Name: "opencraft.yaml"},
	{Ref: "assets/inference.yaml", Name: "inference.yaml"},
	{Ref: "assets/graphs/assistant.yaml", Name: "graphs/assistant.yaml"},
	{Ref: "assets/graphs/node/world.js", Name: "graphs/node/world.js"},
	{Ref: "assets/prompts/system.md", Name: "prompts/system.md"},
}

// FS returns the embedded deploy asset filesystem (for embed sources
// referenced from opencraft.yaml, e.g. the graph definition).
func FS() embed.FS { return assets }

// EmbeddedOpenCraft returns the internal deploy document.
func EmbeddedOpenCraft() ([]byte, error) {
	return assets.ReadFile("assets/opencraft.yaml")
}
