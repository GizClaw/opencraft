package pet

import _ "embed"

// assistantDefaultRiv embeds the shipped assistant character runtime
// asset: the artboard `Pet`, its state machine `PetSM` and the view
// model `PetVM` that assets/assistant-default.pack.json drives.
//
// Do not hand-edit it. The bytes are derived from an input-driven base
// asset by an asset migration generator; neither the base nor the
// generator is part of the repository, so regenerating is a local,
// out-of-band step and only the resulting bytes are committed here.
// The generator asserts that every object other than the state
// machine/view model machinery is byte-identical between the base and
// the output, so the migration cannot drift visually.
//
// assets/README.md records what the migration has to preserve; the
// pack/asset contract itself is pinned by the frontend test
// frontend/src/pet/assetContract.test.ts, which loads these bytes with
// the official Rive runtime.
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
