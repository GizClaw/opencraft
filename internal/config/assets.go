// Package config owns opencraft's embedded deploy assets: the internal
// base opencraft.yaml document, the seeded user layer, the execution
// config, the graph definition, and prompt templates.
package config

import "embed"

// Default deploy assets. The base opencraft.yaml stays embedded as
// internal wiring; the user layer and inference routing are seeded
// into ~/.opencraft/config/ so they are editable at runtime. The graph
// definition and its node scripts/prompt are embedded too — they are
// only read from disk when a config layer overrides the graph
// reference with a {file: ...} source.
//
//go:embed assets/opencraft.yaml assets/user_opencraft.yaml assets/inference.yaml assets/graphs/assistant.yaml assets/graphs/nodes/world.js assets/graphs/nodes/compact.js assets/graphs/prompts/system.md
var assets embed.FS

// UserAssets are the embedded files seeded into ~/.opencraft/config/
// (embed ref -> on-disk name). Only the config documents need a disk
// copy: the deploy loader resolves file sources strictly, so the user
// layer and the router's inference.yaml must exist. The default graph
// is deliberately absent — it resolves from the embedded FS unless the
// user overrides the graph reference.
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
