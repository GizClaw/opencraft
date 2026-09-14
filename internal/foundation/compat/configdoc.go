package compat

import "strings"

// This file owns the shapes of the user configuration layer
// (~/.opencraft/config/opencraft.yaml) that an older build wrote and
// today's drivers reject. The layer is the one document a user may
// hand-edit, so it outlives the builds that wrote it.
//
// Detection is predicate-based rather than version-stamped. The layer
// is deep-merged with the freshly rendered document on every settings
// write (config.mergeUserLayer), which re-emits the file from the
// rendered document: a version kept in a comment would not survive the
// merge, and one kept in a key would have to pass flowcraft's strict
// deployment decode. A predicate reads exactly the shape it retires,
// and a layer that matches none is left byte-for-byte alone.
//
// Rewriting is all-or-nothing: a match hands the whole layer to the
// canonical writer, which re-emits it from the typed configuration. A
// shape therefore describes what it recognises, not how to transform
// it.

// UserLayerShape is one retired shape of the user configuration layer.
type UserLayerShape struct {
	// Name identifies the shape, e.g. in a test failure.
	Name string
	// Needs reports whether one layer document still carries the
	// shape.
	Needs func(data []byte) bool
}

// UserLayerShapes lists the retired user-layer shapes, oldest first.
// Add one entry when a release stops accepting a key, and drop the
// entry once no install that could still carry it upgrades in place.
var UserLayerShapes = []UserLayerShape{
	// Deprecated model keys: provider model keys the flowcraft 0.2.7
	// driver contract removed — effort_none (reasoning off is implied
	// by `reasoning: toggle`), per-model responses (the provider-level
	// api owns the surface) and top-level dimensions (embed dimensions
	// moved into capabilities). Strict driver decoding rejects them.
	{
		Name:  "deprecated-model-keys",
		Needs: hasDeprecatedModelKeys,
	},
}

// ShapeNeedsRewrite reports the retired shapes one user layer document
// still carries, oldest first. An empty result means the document is
// already canonical.
func ShapeNeedsRewrite(data []byte) []string {
	var found []string
	for _, shape := range UserLayerShapes {
		if shape.Needs(data) {
			found = append(found, shape.Name)
		}
	}
	return found
}

// deprecatedModelKeys are the model keys the canonical writer drops.
// The match is textual because the layer is read as a document that
// may not parse: the migration runs before the layer is decoded, so a
// key the drivers reject cannot hide behind a parse failure.
var deprecatedModelKeys = []string{
	"effort_none:",
	"responses:",
	"dimensions:",
}

// hasDeprecatedModelKeys reports whether one user layer still names a
// key the canonical writer drops.
func hasDeprecatedModelKeys(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		for _, key := range deprecatedModelKeys {
			if strings.HasPrefix(trimmed, key) {
				return true
			}
		}
	}
	return false
}
