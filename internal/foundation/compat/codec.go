package compat

import "strings"

// This file owns the compatibility rules of the *configuration codec*:
// the shapes a provider spec may still arrive in, judged before the
// document is decoded by flowcraft's strict driver decode. The shape
// rules of whole user-layer documents live in configdoc.go; the
// retired spellings of a spec key live here next to the fold that
// rewrites them.

// RetiredProviderSpecKeys are provider spec keys retired by the drivers.
// A document that still carries one is dropped rather than parked in the
// opaque provider bag: the bag is re-emitted verbatim, so keeping the key
// would fail every deployment build with a message about a field the
// user never wrote.
var RetiredProviderSpecKeys = map[string]bool{
	// catalog selected a driver model namespace; core v0.4.0 ships no
	// built-in line-up and rejects the key.
	"catalog": true,
}

// NormalizeProviderSpec folds legacy top-level provider spec keys into
// the shape the unified drivers expect. `chat_stream_options` moved
// under `wire` when the OpenAI wire family gained one driver; a bag
// written against the old layout is rewritten rather than left to be
// rejected by flowcraft's strict provider decode.
func NormalizeProviderSpec(spec map[string]any) map[string]any {
	options, ok := spec["chat_stream_options"]
	if !ok {
		return spec
	}
	out := make(map[string]any, len(spec))
	for key, value := range spec {
		if key != "chat_stream_options" {
			out[key] = value
		}
	}
	wire, _ := out["wire"].(map[string]any)
	if wire == nil {
		wire = make(map[string]any, 1)
	}
	wire["chat_stream_options"] = options
	out["wire"] = wire
	return out
}

// LegacyPluginKeyRef reports whether keyValue points into the secret
// namespace of the plugin whose id is stableID ("auth/<stableID>/...").
// Every plugin inference row written under the pre-ownership contract
// had this shape, because the host required instance ids to equal the
// plugin id and key references to stay inside the plugin's namespace,
// so a row matching both shapes could only have been created by the
// plugin with that id.
func LegacyPluginKeyRef(stableID, keyValue string) bool {
	return stableID != "" && strings.HasPrefix(keyValue, "auth/"+stableID+"/")
}
