// Application deployment documents: the layer stack an application
// runtime assembles from — the embedded contract layer, the
// application's own layers, the capability fragments its manifest
// enabled with the host's wiring above them, and the host-generated
// inference overlay.
package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
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
	// Capabilities are the host-owned capability fragments the manifest
	// opted into, in manifest order: the tool containers, the sandbox
	// and the network gate an application asks for by name (see
	// config.AppDeployLayers). Empty means the application runs on the
	// contract layer's own surface.
	Capabilities []string
	// Agents are every agent the application runs, entry first — the
	// list Host.identity() carries, built from the manifest's agent and
	// agents fields. The generated capability wiring names each of
	// them, because a dependency is per agent.
	Agents []string
}

// LoadAppDocument merges one application's deploy document: the
// embedded contract layer, the application's layers, the capability
// fragments the manifest enabled with the wiring that makes them
// reachable (config.AppDeployLayers), and the host-generated inference
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
	// order. Both bands, and the host's own on top of them, come from
	// foundation/config — which is also where the registry's preflight
	// merges exactly the same stack, so the document a refusal is about
	// is the document that gets assembled.
	layers, err := config.AppDeployLayers(
		app.ContentDir, app.Layers, app.Capabilities, app.Agents)
	if err != nil {
		return deploy.Document{}, fmt.Errorf("engine: %s: %w", appLabel(app), err)
	}
	overlay, ok, err := config.UserInferenceOverlay(userDir)
	if err != nil {
		return deploy.Document{}, err
	}
	if ok {
		layers = append(layers, deploy.Layer{
			Priority: config.AppOverlayPriorityBase,
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
