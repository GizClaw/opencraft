package compat

import "strings"

// This file owns the compatibility rules of the *configuration codec*:
// the shapes a provider spec may still arrive in, judged before the
// document is decoded by flowcraft's strict driver decode. The shape
// rules of whole user-layer documents live in configdoc.go; the
// retired spellings of a spec key live here next to the fold that
// rewrites them.

// LegacyProviderSpec carries provider-spec leaves an older build wrote
// at the top level of a deployment spec. The writer emits every leaf in
// its current placement, and opencraft models the whole provider spec
// with typed fields, so this type exists only to read a document written
// before the fold moved.
type LegacyProviderSpec struct {
	// ChatStreamOptions moved under wire when the OpenAI wire family
	// gained one driver; the loader folds it into the typed knobs.
	ChatStreamOptions *LegacyChatStreamOptions `json:"chat_stream_options,omitempty"`
}

// LegacyChatStreamOptions is the pre-wire placement of the OpenAI chat
// stream options.
type LegacyChatStreamOptions struct {
	IncludeUsage       *bool `json:"include_usage,omitempty"`
	IncludeObfuscation *bool `json:"include_obfuscation,omitempty"`
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
