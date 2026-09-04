// Package config owns opencraft's user-facing configuration: discovery,
// seeding, layered loading (embedded base -> user), and the
// app-level execution document. The deploy layering itself is
// flowcraft core's deploy.LoadLayers.
package config

import (
	"context"
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

// View is the merged deployment document.
type View struct {
	// Document is the deploy document merged across layers.
	Document deploy.Document
}

// Load merges the layered deploy documents.
func (m *Manager) Load(ctx context.Context) (*View, error) {
	layers := []deploy.Layer{{
		Priority: 0,
		Name:     "embedded",
		Source:   resource.Source{Embed: "assets/opencraft.yaml"},
		Embed:    FS(),
	}, {
		// Fixed inference wiring (providers + infer assembly + router
		// retry shell) lives in its own embedded layer so the
		// setup-written user layer only carries the variable parts
		// (key profiles, azure, generate targets).
		Priority: 1,
		Name:     "embedded-inference",
		Source:   resource.Source{Embed: "assets/inference.yaml"},
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
	if m.explicit != "" {
		// Above embedded, below the user layer: -config is meant to
		// override built-ins, but the user layer still wins for the
		// keys it sets.
		layers = append(layers, deploy.Layer{
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
		layers = append(layers, deploy.Layer{
			Priority: 10,
			Name:     string(LayerUser),
			Source:   resource.Source{File: "opencraft.yaml"},
			BaseDir:  m.userDir,
		})
	}
	doc, _, err := deploy.LoadLayers(ctx, layers)
	if err != nil {
		return nil, err
	}
	return &View{
		Document: doc,
	}, nil
}
