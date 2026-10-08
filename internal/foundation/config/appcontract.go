package config

import (
	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"
)

// The embedded application contract layer and the priority band every
// other layer of an application deployment sits in.
//
// The numbers and the asset name live here rather than at the two call
// sites that assemble them because there are two: the assembly
// (engine.LoadAppDocument) merges the three layers into a runtime, and
// the registry's preflight (capabilities/apps) merges the contract and
// the application's own layers to decide whether they may be installed
// at all. A refusal that disagreed with what actually gets assembled
// would be the worst kind of validation.
const (
	// AppContractAsset names the embedded layer file: the complete
	// document (version and all) at the bottom of every application
	// deployment.
	AppContractAsset = "assets/app.yaml"
	// AppLayerPriorityBase is the priority of the first application
	// layer; the manifest's remaining layers follow it in order.
	AppLayerPriorityBase = 10
	// AppCapabilityPriorityBase is the priority of the shared tool
	// assembly layer; the capability fragments the manifest enabled
	// follow it in order. The band sits above every application layer
	// whatever their count — which is what "an application cannot
	// shadow a fragment" means in practice: a layer that declared a
	// fragment's resource would lose the merge to it, so the preflight
	// refuses that key instead (see capabilities/apps).
	AppCapabilityPriorityBase = 100
	// AppWiringPriorityBase is the priority of the host-generated
	// wiring layer, above the fragments it names.
	AppWiringPriorityBase = 300
	// AppOverlayPriorityBase is the priority of the host-generated
	// inference layer, the topmost layer of every application
	// deployment.
	AppOverlayPriorityBase = 400
)

// AppLayerName reports the layer a provenance entry belongs to when that
// layer is one of the application's own, and "" for every host band: the
// contract layer, the capability fragments, the generated wiring and the
// inference overlay. A refusal must not claim an application file
// declared a key the host did, which is what the empty answer means.
func AppLayerName(ref deploy.LayerRef) string {
	if ref.Priority < AppLayerPriorityBase || ref.Priority >= AppCapabilityPriorityBase {
		return ""
	}
	return ref.Name
}

// AppContractLayer returns the lowest-priority layer of an application
// deployment: the embedded contract layer, the whole of what v1 gives
// an application. It is the only place that spells the layer out, so
// the assembly and the preflight cannot drift onto different assets.
func AppContractLayer() deploy.Layer {
	return deploy.Layer{
		Priority: 0,
		Name:     "contract",
		Source:   resource.Source{Embed: AppContractAsset},
		Embed:    FS(),
	}
}
