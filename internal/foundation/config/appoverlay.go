package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/utils"
)

// The host-generated inference layer of an application deployment.
//
// An application document is merged from the embedded contract layer,
// the application's own layers, and one layer the host generates: the
// user's inference wiring (the router policy, the infer assembly, and
// every provider.* declaration), rendered as a partial document. The
// key set is the write side's, not a second list — the settings page
// owns exactly these keys, so the overlay carries exactly them, no
// matter what else a hand-edited user layer accumulates.

// providerKeyPrefix names the per-instance provider resources the
// inference writer generates (provider.<id>). The prefix is one
// spelling across the package: the writer drops it wholesale, the
// loader reads it back, and the overlay copies it.
const providerKeyPrefix = "provider."

// AppendInferenceOverlay copies every inference-owned resource of src
// into dst and reports whether it moved anything. Nothing else moves:
// the overlay is a layer of inference wiring, so agents, the runtime
// section and the document version stay where they were (a later layer
// may be partial, and the version belongs to the layers below it).
func AppendInferenceOverlay(dst *deploy.Document, src deploy.Document) bool {
	if dst == nil {
		return false
	}
	moved := false
	for key, res := range src.Resources {
		if !OwnsInferenceKey(key) {
			continue
		}
		if dst.Resources == nil {
			dst.Resources = make(resource.Resources, 3)
		}
		dst.Resources[key] = res
		moved = true
	}
	return moved
}

// UserInferenceOverlay renders the user layer's inference declarations
// as the JSON partial document an application deployment carries as its
// host-generated layer: ready for resource.Source{Inline: ...}, and
// empty (ok=false) when the user layer is absent or declares no
// inference wiring — the "not configured yet" state the settings page
// owns, which BuildRuntime then reports as a missing router.
//
// It reads the same file the assistant deployment reads, through the
// same gates: the legacy-shape rewrite runs first (so an upgraded
// installation does not hand the app path a document the writer no
// longer emits), and a retired ${env:OPEN_CRAFT_*} reference fails here
// the way it fails a config load — naming the file and the replacement,
// instead of surfacing from inside deployment as an unset variable.
func UserInferenceOverlay(configDir string) ([]byte, bool, error) {
	if _, err := MigrateUserInferenceConfig(configDir); err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(UserLayerFile(configDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("config: read user layer: %w", err)
	}
	refs, err := FindRetiredRefs(configDir)
	if err != nil {
		return nil, false, err
	}
	if len(refs) > 0 {
		return nil, false, retiredRefsError(configDir, refs)
	}
	// Partial semantics, exactly like the layer the manager loads: the
	// user layer carries a version today, but an older or hand-written
	// one need not, and this document is never the lowest-priority
	// layer.
	doc, err := utils.Decode[deploy.Document](data)
	if err != nil {
		return nil, false, fmt.Errorf("config: parse user layer: %w", err)
	}
	overlay := deploy.Document{}
	if !AppendInferenceOverlay(&overlay, doc) {
		return nil, false, nil
	}
	blob, err := json.Marshal(overlay)
	if err != nil {
		return nil, false, fmt.Errorf("config: encode inference overlay: %w", err)
	}
	return blob, true, nil
}

// OwnsInferenceKey reports whether one resource key belongs to the
// inference wiring the settings page generates: the router policy and
// the infer assembly (managedResourceKeys, the set the write side
// replaces wholesale) plus every provider.* declaration (the write side
// drops and re-emits the whole prefix).
//
// It is exported because the application registry asks the same
// question when it refuses an application layer that declares the
// user's inference itself: the keys the host generates are exactly the
// keys an application may not own, and the answer has to come from the
// write side's set rather than a second list.
func OwnsInferenceKey(key string) bool {
	return managedResourceKeys()[key] || strings.HasPrefix(key, providerKeyPrefix)
}
