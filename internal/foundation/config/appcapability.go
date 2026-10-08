// Capability fragments: the host-owned deployment layers an application
// opts into by naming them in its manifest's capabilities: list.
//
// An application's own layers are narrow by policy (capabilities/apps
// reads every key and kind of them); a fragment is the other half of
// that boundary — written by the host, merged above the application's
// layers, and the only way an application gets a surface v1 does not
// hand out by default: tool containers, the sandbox, the network gate.
// The name in the manifest is the whole request, and an application
// cannot widen what the fragment grants (its layers cannot declare a
// fragment's resource keys or kinds).
//
// One fragment is one embedded layer (assets/capabilities/<name>.yaml)
// declaring its own resources and, under the shared tool assembly's key,
// the containers it contributes (resources.tools.deps, unioned by the
// core merge). What the host generates on top is one more layer: the
// wiring that points every agent the application runs at that assembly
// (AppDeployLayers).
package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
)

// CapabilityToolsKey is the resource key of the tool assembly the
// fragments build together. It is also the dependency name every agent
// engine reads it under — flowcraft's graph engine requires exactly this
// name for its "tool" node — so the key and the dep name are spelled
// once, here.
const CapabilityToolsKey = "tools"

// capabilityTable is every fragment this build ships. The names are the
// manifest vocabulary: adding, renaming or dropping one is a change to
// what an installed application may write, so the table is short and
// explicit, and the preflight (capabilities/apps) reads it rather than
// only the assembly. What each name stands for:
//
//   - "tools": the host's file tools on the application's own private
//     workspace (read_file, write_file, list_dir, grep, glob,
//     apply_patch, view_image). The content root stays read-only and
//     the user's own directories are not reachable.
//   - "exec": sandboxed command execution (exec_command), with the
//     application's own approvals file and audit trail.
//   - "web": web_fetch and web_search, under the host's network policy.
//
// The layer behind each name is assets/capabilities/<name>.yaml, which
// is where the surface itself is spelled out — the list above says what
// a user is agreeing to when they install a package that names one.
var capabilityTable = []string{"tools", "exec", "web"}

// CapabilityNames lists every fragment name, sorted, for the refusal
// that has to say what a manifest could have written instead.
func CapabilityNames() []string {
	names := append([]string(nil), capabilityTable...)
	sort.Strings(names)
	return names
}

// KnownCapability reports whether this build ships a fragment under
// name.
func KnownCapability(name string) bool {
	for _, known := range capabilityTable {
		if known == name {
			return true
		}
	}
	return false
}

// capabilityAsset names the embedded layer of one fragment. The file
// name follows the capability name, so the table above cannot list a
// fragment whose layer is spelled somewhere else.
func capabilityAsset(name string) string {
	return "assets/capabilities/" + name + ".yaml"
}

// capabilityAssemblyAsset is the layer carrying the policy every
// fragment's containers share: the tool result chain (recover, timeout,
// result limit, truncate, redact, audit) declared once, above the
// application's layers and below the fragments that add their containers
// to it.
const capabilityAssemblyAsset = "assets/capabilities/assembly.yaml"

// AppDeployLayers returns the layer stack of one application deployment:
// the embedded contract layer (priority 0), the application's own layers
// (AppLayerPriorityBase, in manifest order, relative to root), and — when
// the manifest opted into capabilities — the shared tool assembly, the
// capability fragments (AppCapabilityPriorityBase) and the host-generated
// wiring that makes them reachable from the application's agents.
//
// It is the one composition of these bands. engine.LoadAppDocument adds
// the inference overlay above it and builds a runtime; the registry's
// preflight (capabilities/apps) merges exactly this stack, minus the
// overlay, to answer whether the application may be installed at all. A
// refusal that disagreed with what actually gets assembled would be the
// worst kind of validation, so both read the layers from here.
//
// agents is every agent the application runs, entry first: the wiring
// names each of them, because a dependency is per agent and an
// application's other agents each declare their own engine. A capability
// that reaches no agent would be a request recorded and never answered,
// so an empty list next to a non-empty capability list is an error
// rather than a silent no-op.
func AppDeployLayers(
	root string,
	layers []string,
	capabilities []string,
	agents []string,
) ([]deploy.Layer, error) {
	stack := []deploy.Layer{AppContractLayer()}
	for index, name := range layers {
		// A manifest's layer list is validated on import, but this is
		// the join onto the content root: a layer name that could walk
		// out of it (or an absolute path) is refused here, no matter
		// which caller assembled the list.
		if strings.TrimSpace(name) == "" || !pathsafe.RelRef(name) {
			return nil, fmt.Errorf(
				"config: layer %q is not a relative path inside the content root", name)
		}
		stack = append(stack, deploy.Layer{
			Priority: AppLayerPriorityBase + index,
			Name:     name,
			Source:   resource.Source{File: name},
			BaseDir:  root,
		})
	}
	if len(capabilities) == 0 {
		return stack, nil
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf(
			"config: %d capabilities enabled but no agent to wire them into",
			len(capabilities))
	}
	stack = append(stack, deploy.Layer{
		Priority: AppCapabilityPriorityBase,
		Name:     "capabilities",
		Source:   resource.Source{Embed: capabilityAssemblyAsset},
		Embed:    FS(),
	})
	for index, name := range capabilities {
		name = strings.TrimSpace(name)
		if !KnownCapability(name) {
			return nil, fmt.Errorf(
				"config: %q is not a capability this build provides (want %s)",
				name, strings.Join(CapabilityNames(), ", "))
		}
		stack = append(stack, deploy.Layer{
			Priority: AppCapabilityPriorityBase + 1 + index,
			Name:     "capability:" + name,
			Source:   resource.Source{Embed: capabilityAsset(name)},
			Embed:    FS(),
		})
	}
	wiring, err := capabilityWiringLayer(agents)
	if err != nil {
		return nil, err
	}
	return append(stack, wiring), nil
}

// capabilityWiringLayer renders the host-generated layer above the
// fragments: one dependency per agent, pointing that agent's engine at
// the tool assembly the fragments declared.
//
// The deps live here rather than in the fragments because an agent's
// dependencies are per agent and the agent names come from the manifest:
// a static layer can only name the contract's slot, and a fragment that
// named an agent would be a fragment about one application's agents.
// This layer sits above every application layer, so a layer that pointed
// its engine somewhere else is overridden rather than obeyed — the tool
// assembly an application may reach is the one the host built.
func capabilityWiringLayer(agents []string) (deploy.Layer, error) {
	wiring := make(map[string]any, len(agents))
	for _, name := range agents {
		if strings.TrimSpace(name) == "" {
			return deploy.Layer{}, fmt.Errorf(
				"config: capability wiring: agent name is empty")
		}
		wiring[name] = map[string]any{
			"engine": map[string]any{
				"deps": map[string]any{CapabilityToolsKey: CapabilityToolsKey},
			},
		}
	}
	blob, err := json.Marshal(map[string]any{"agents": wiring})
	if err != nil {
		return deploy.Layer{}, fmt.Errorf("config: encode capability wiring: %w", err)
	}
	return deploy.Layer{
		Priority: AppWiringPriorityBase,
		Name:     "capability-wiring",
		Source:   resource.Source{Inline: blob},
	}, nil
}
