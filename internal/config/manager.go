// Package config owns opencraft's user-facing configuration: discovery,
// seeding, layered loading (embedded base -> user -> project), and the
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
	LayerUser    Layer = "user"
	LayerProject Layer = "project"
)

// Options configures a Manager.
type Options struct {
	// WorkDir is the directory configuration is discovered from.
	// Defaults to the current working directory.
	WorkDir string
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

// Manager owns layered configuration loading: the embedded base, the
// user layer, and the optional project layer.
type Manager struct {
	workDir    string
	userDir    string
	projectDir string
	explicit   string
}

// Open creates a Manager for workDir, discovering the project layer.
func Open(opts Options) (*Manager, error) {
	workDir := opts.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	userDir := opts.UserDir
	var err error
	if userDir == "" {
		userDir, err = UserConfigDir()
		if err != nil {
			return nil, err
		}
	}
	projectDir, _ := ProjectConfigDir(workDir)
	return &Manager{
		workDir:    workDir,
		userDir:    userDir,
		projectDir: projectDir,
		explicit:   opts.Explicit,
	}, nil
}

// WorkDir returns the directory configuration was discovered from.
func (m *Manager) WorkDir() string { return m.workDir }

// UserDir returns the global user configuration directory.
func (m *Manager) UserDir() string { return m.userDir }

// ProjectDir returns the discovered project layer directory, or "" when
// no project layer exists.
func (m *Manager) ProjectDir() string { return m.projectDir }

// View is the merged deployment plus provenance.
type View struct {
	// Document is the deploy document merged across layers.
	Document deploy.Document
	// Provenance records which layer provided each resource/agent.
	Provenance deploy.Provenance
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
	}}
	if m.explicit != "" {
		// Above embedded, below the user layer: -config is meant to
		// override built-ins, but user/project layers still win for
		// the keys they set.
		layers = append(layers, deploy.Layer{
			Priority: 5,
			Name:     "explicit",
			Source:   resource.Source{File: filepath.Base(m.explicit)},
			BaseDir:  filepath.Dir(m.explicit),
		})
	}
	// The user layer is created by the first-run setup wizard; before
	// that (or after a reset) it is absent and the embedded base alone
	// has no inference wiring — main gates startup behind the wizard.
	if _, err := os.Stat(filepath.Join(m.userDir, "opencraft.yaml")); err == nil {
		layers = append(layers, deploy.Layer{
			Priority: 10,
			Name:     string(LayerUser),
			Source:   resource.Source{File: "opencraft.yaml"},
			BaseDir:  m.userDir,
		})
	}
	if m.projectDir != "" {
		layers = append(layers, deploy.Layer{
			Priority: 20,
			Name:     string(LayerProject),
			Source:   resource.Source{File: "opencraft.yaml"},
			BaseDir:  m.projectDir,
		})
	}
	doc, provenance, err := deploy.LoadLayers(ctx, layers)
	if err != nil {
		return nil, err
	}
	return &View{
		Document:   doc,
		Provenance: provenance,
	}, nil
}

// Validate loads and strictly parses every layer and document.
func (m *Manager) Validate(ctx context.Context) error {
	_, err := m.Load(ctx)
	return err
}
