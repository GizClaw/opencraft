package pet

import _ "embed"

// assistantDefaultRiv embeds the shipped assistant character runtime
// asset. The editable source lives next to it as
// assets/assistant-default.scene.json; regenerate the binary from that
// spec with the local Rive scene pipeline.
//
//go:embed assets/assistant-default.riv
var assistantDefaultRiv []byte

// BuiltinAssistantPackAsset returns a copy of the builtin assistant
// .riv bytes so callers can deliver them to the Rive runtime without
// aliasing the embedded slice.
func BuiltinAssistantPackAsset() []byte {
	out := make([]byte, len(assistantDefaultRiv))
	copy(out, assistantDefaultRiv)
	return out
}
