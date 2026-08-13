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
	// Explicit overrides the base embedded deploy document with an
	// on-disk full document (the -config flag).
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
	}}
	if m.explicit != "" {
		layers[0] = deploy.Layer{
			Priority: 0,
			Name:     "explicit",
			Source:   resource.Source{File: filepath.Base(m.explicit)},
			BaseDir:  filepath.Dir(m.explicit),
		}
	}
	layers = append(layers, deploy.Layer{
		Priority: 10,
		Name:     string(LayerUser),
		Source:   resource.Source{File: "opencraft.yaml"},
		BaseDir:  m.userDir,
	})
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
