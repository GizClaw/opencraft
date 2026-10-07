// Application deployment documents: the three layers an application
// runtime assembles from — the embedded contract layer, the
// application's own layers, and the host-generated inference overlay.
package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
)

// AppDoc names the deployment content of one installed application: the
// directory its layers were installed into, and the layers themselves in
// ascending priority order. It is deliberately a plain value: the
// application registry (capabilities/apps) owns manifests, ids and
// validation, and maps its own spec onto this shape at assembly time.
type AppDoc struct {
	// ID is the installed application id. It only appears in error
	// text; the state layout is built from it separately.
	ID string
	// ContentDir is the application's content root: the layer files and
	// every {file: ...} reference inside them resolve against it.
	ContentDir string
	// Layers are the application's deployment layers, in ascending
	// priority order, relative to ContentDir.
	Layers []string
}

// LoadAppDocument merges one application's deploy document: the
// embedded contract layer (priority 0), the application's layers
// (priority 10+, manifest order), and the host-generated inference
// overlay (above all of them) built from the user layer's inference
// wiring in userDir.
//
// The overlay is the only place inference comes from: an application
// document has no router, no infer assembly and no provider of its own,
// so a missing overlay stays a missing router — BuildRuntime reports
// that as the settings page's business, the same way it does for the
// assistant.
func LoadAppDocument(
	ctx context.Context,
	app AppDoc,
	userDir string,
) (deploy.Document, error) {
	if strings.TrimSpace(app.ContentDir) == "" {
		return deploy.Document{}, fmt.Errorf(
			"engine: %s: content dir is required", appLabel(app))
	}
	if len(app.Layers) == 0 {
		return deploy.Document{}, fmt.Errorf(
			"engine: %s declares no deployment layers", appLabel(app))
	}
	// The contract layer is the complete document (version and all), so
	// it has to be first; the application's layers follow it in manifest
	// order; the inference overlay goes above every one of them, whatever
	// the manifest declares. The layer and both bands come from
	// foundation/config, which is also where the registry's preflight
	// merges the same contract — see AppContractLayer.
	layers := []deploy.Layer{config.AppContractLayer()}
	for index, name := range app.Layers {
		// A manifest's layer list is validated on import, but this is
		// the join onto the content root: a layer name that could walk
		// out of it (or an absolute path) is refused here, no matter
		// which caller assembled the list.
		if strings.TrimSpace(name) == "" || !pathsafe.RelRef(name) {
			return deploy.Document{}, fmt.Errorf(
				"engine: %s: layer %q is not a relative path inside the content root",
				appLabel(app), name)
		}
		layers = append(layers, deploy.Layer{
			Priority: config.AppLayerPriorityBase + index,
			Name:     name,
			Source:   resource.Source{File: name},
			BaseDir:  app.ContentDir,
		})
	}
	overlay, ok, err := config.UserInferenceOverlay(userDir)
	if err != nil {
		return deploy.Document{}, err
	}
	if ok {
		layers = append(layers, deploy.Layer{
			Priority: config.AppOverlayPriorityBase + len(app.Layers),
			Name:     "inference",
			Source:   resource.Source{Inline: overlay},
		})
	}
	doc, _, err := deploy.LoadLayers(ctx, layers)
	if err != nil {
		return deploy.Document{}, fmt.Errorf("engine: %s: %w", appLabel(app), err)
	}
	return doc, nil
}

// appLabel names the application in error text, tolerating a caller
// that has not resolved an id yet.
func appLabel(app AppDoc) string {
	if strings.TrimSpace(app.ID) == "" {
		return "application"
	}
	return "application " + app.ID
}
