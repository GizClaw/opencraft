package app

import (
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/deploy"
)

// normalizeGraphOverride reconciles the deploy merge's treatment of the
// agent engine graph source. Layer settings deep-merge, so a user
// override graph: {file: graphs/my.yaml} over the embedded default
// graph: {embed: assets/graphs/assistant.yaml} lands as a two-key
// object. flowcraft's source parser treats multi-key objects as inline
// content instead of a reference, which would make the graph build
// fail with an "unknown field" decode error. The explicit user file
// wins, so the merged reference is reduced to it. Single-key
// references (the embedded default or an override) are untouched.
func normalizeGraphOverride(doc deploy.Document) {
	for name, def := range doc.Agents {
		if len(def.Engine.Settings) == 0 {
			continue
		}
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(def.Engine.Settings, &settings); err != nil {
			continue
		}
		raw, ok := settings["graph"]
		if !ok || len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var ref map[string]json.RawMessage
		if err := json.Unmarshal(raw, &ref); err != nil || len(ref) < 2 {
			continue
		}
		fileRaw, hasFile := ref["file"]
		if !hasFile {
			continue
		}
		settings["graph"] = json.RawMessage(`{"file":` + string(fileRaw) + `}`)
		merged, err := json.Marshal(settings)
		if err != nil {
			continue
		}
		def.Engine.Settings = merged
		doc.Agents[name] = def
	}
}
