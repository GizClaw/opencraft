// The user-facing configuration manager: discovery, seeding, layered
// loading (embedded base -> user), and the app-level execution
// document. The package as a whole is described in doc.go.
package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"
)

// Layer identifies a configuration layer.
type Layer string

const (
	LayerUser Layer = "user"
)

// Options configures a Manager.
type Options struct {
	// UserDir overrides the global user configuration directory
	// (defaults to ~/.opencraft/config).
	UserDir string
	// Explicit adds an on-disk deploy document as a layer above the
	// embedded base (the -config flag). It may be partial: resources it
	// does not name keep the embedded defaults, and anything it names
	// overrides them. A full document therefore behaves like a wholesale
	// replacement, while a partial document no longer silently drops
	// every built-in default (the pre-merge behavior).
	Explicit string
}

// Manager owns layered configuration loading: the embedded base and
// the user layer.
type Manager struct {
	userDir  string
	explicit string
}

// Open creates a Manager rooted at the user configuration directory.
func Open(opts Options) (*Manager, error) {
	userDir := opts.UserDir
	var err error
	if userDir == "" {
		userDir, err = UserConfigDir()
		if err != nil {
			return nil, err
		}
	}
	return &Manager{
		userDir:  userDir,
		explicit: opts.Explicit,
	}, nil
}

// View is the merged deployment document and the layers it came from.
type View struct {
	// Document is the deploy document merged across layers.
	Document deploy.Document
	// Base is the embedded layer list: the assets this binary ships,
	// in merge order. A craft-based assembly names them in its
	// definition, apart from the on-disk overlay that changes under
	// the running process.
	Base []deploy.Layer
	// Overlay is the on-disk layer list above the base: the -config
	// document and the user layer, in merge order (empty when neither
	// exists). It is the half a reload re-reads.
	Overlay []deploy.Layer
}

// embeddedLayers is the layer list of the assets compiled into the
// binary, lowest priority first.
func embeddedLayers() []deploy.Layer {
	return []deploy.Layer{{
		Priority: 0,
		Name:     "embedded",
		Source:   resource.Source{Embed: "assets/opencraft.yaml"},
		Embed:    FS(),
	}, {
		// Tool containers + script runtime, agent definitions, and the
		// runtime section live in their own embedded layer files so the
		// base document stays navigable. Each is a partial document
		// deep-merged over the base (only the first layer is required
		// to carry version).
		Priority: 2,
		Name:     "embedded-tools",
		Source:   resource.Source{Embed: "assets/tools.yaml"},
		Embed:    FS(),
	}, {
		Priority: 3,
		Name:     "embedded-agents",
		Source:   resource.Source{Embed: "assets/agents.yaml"},
		Embed:    FS(),
	}, {
		Priority: 4,
		Name:     "embedded-runtime",
		Source:   resource.Source{Embed: "assets/runtime.yaml"},
		Embed:    FS(),
	}}
}

// Load merges the layered deploy documents.
func (m *Manager) Load(ctx context.Context) (*View, error) {
	base := embeddedLayers()
	overlay := make([]deploy.Layer, 0, 2)
	if m.explicit != "" {
		// Above embedded, below the user layer: -config is meant to
		// override built-ins, but the user layer still wins for the
		// keys it sets.
		overlay = append(overlay, deploy.Layer{
			Priority: 5,
			Name:     "explicit",
			Source:   resource.Source{File: filepath.Base(m.explicit)},
			BaseDir:  filepath.Dir(m.explicit),
		})
	}
	// The user layer is written by the settings page; before that (or
	// after a reset) it is absent and the embedded base alone has no
	// inference wiring — the UI guides the user to the settings page.
	if _, err := os.Stat(filepath.Join(m.userDir, "opencraft.yaml")); err == nil {
		if _, err := MigrateUserInferenceConfig(m.userDir); err != nil {
			return nil, fmt.Errorf("config: migrate user inference layer: %w", err)
		}
		// Retired ${env:OPEN_CRAFT_*} references survive upgrades in
		// hand-authored layers (they were correct before the resolver
		// swap) and cannot expand any more. Fail here, naming the file
		// and the replacement, instead of letting deployment report an
		// unset environment variable that points at neither.
		refs, err := FindRetiredRefs(m.userDir)
		if err != nil {
			return nil, fmt.Errorf("config: inspect user layer: %w", err)
		}
		if len(refs) > 0 {
			return nil, retiredRefsError(m.userDir, refs)
		}
		overlay = append(overlay, deploy.Layer{
			Priority: 10,
			Name:     string(LayerUser),
			Source:   resource.Source{File: "opencraft.yaml"},
			BaseDir:  m.userDir,
		})
	}
	// The base is the definition's half — fixed for this binary — and
	// the overlay is the half a reload re-reads; merging them is what
	// both halves have to agree on, so it happens in one place.
	layers := make([]deploy.Layer, 0, len(base)+len(overlay))
	layers = append(layers, base...)
	layers = append(layers, overlay...)
	doc, _, err := deploy.LoadLayers(ctx, layers)
	if err != nil {
		return nil, err
	}
	return &View{
		Document: doc,
		Base:     base,
		Overlay:  overlay,
	}, nil
}
