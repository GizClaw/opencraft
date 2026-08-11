package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	sdkconfig "github.com/GizClaw/flowcraft/sdk/config"
	inferenceconfig "github.com/GizClaw/flowcraft/sdk/inference/config"
	sandboxconfig "github.com/GizClaw/flowcraft/sdk/sandbox/config"
	toolconfig "github.com/GizClaw/flowcraft/sdk/tool/config"
	workspaceconfig "github.com/GizClaw/flowcraft/sdk/workspace/config"

	"sigs.k8s.io/yaml"
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
	// (defaults to ~/.opencraft/config). Tests and embedded hosts use it
	// to isolate configuration.
	UserDir string
}

// Manager owns configuration loading: it discovers the user and project
// layers, merges them with flowcraft's layered loader, and exposes the
// typed documents to callers (TUI, CLI, app-server, runtime assembly).
type Manager struct {
	workDir    string
	userDir    string
	projectDir string
	loader     *sdkconfig.Loader
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
		loader: sdkconfig.NewLoader(
			sdkconfig.WithBaseDir(userDir),
			sdkconfig.WithEmbed(FS()),
		),
	}, nil
}

// WorkDir returns the directory configuration was discovered from.
func (m *Manager) WorkDir() string { return m.workDir }

// UserDir returns the global user configuration directory.
func (m *Manager) UserDir() string { return m.userDir }

// ProjectDir returns the discovered project layer directory, or "" when
// no project layer exists.
func (m *Manager) ProjectDir() string { return m.projectDir }

// View is the merged, typed configuration plus provenance.
type View struct {
	Inference *inferenceconfig.Document
	Workspace *workspaceconfig.Document
	Tools     *toolconfig.Document
	Sandbox   *sandboxconfig.Document

	// Raw holds the final merged document bytes per file name
	// ("inference.yaml", "tools.yaml", ...).
	Raw map[string][]byte
	// Origins maps "file.leaf.path" to the layer that last set it
	// (user / project); only layered documents (tools, sandbox) carry
	// origins.
	Origins map[string]string
	// Paths maps file name to the effective on-disk path.
	Paths map[string]string
}

// Load reads, merges, and parses every configuration document.
func (m *Manager) Load(ctx context.Context) (*View, error) {
	v := &View{
		Raw:     make(map[string][]byte),
		Origins: make(map[string]string),
		Paths:   make(map[string]string),
	}
	for _, name := range []string{"inference", "workspace"} {
		data, err := os.ReadFile(filepath.Join(m.userDir, name+".yaml"))
		if err != nil {
			return nil, fmt.Errorf("config %s.yaml: %w", name, err)
		}
		v.Raw[name+".yaml"] = data
		v.Paths[name+".yaml"] = filepath.Join(m.userDir, name+".yaml")
		switch name {
		case "inference":
			doc, err := inferenceconfig.Parse(data)
			if err != nil {
				return nil, fmt.Errorf("inference.yaml: %w", err)
			}
			v.Inference = &doc
		case "workspace":
			doc, err := workspaceconfig.Parse(data)
			if err != nil {
				return nil, fmt.Errorf("workspace.yaml: %w", err)
			}
			v.Workspace = &doc
		}
	}
	for _, name := range []string{"tools", "sandbox"} {
		layers := []sdkconfig.Layer{
			{Name: string(LayerUser), Source: sdkconfig.FileSource(name + ".yaml")},
		}
		path := filepath.Join(m.userDir, name+".yaml")
		if m.projectDir != "" {
			projectFile := filepath.Join(m.projectDir, name+".yaml")
			if data, err := os.ReadFile(projectFile); err == nil {
				// Project files live outside the loader's base directory
				// (path confinement), so they merge as literal content.
				layers = append(layers, sdkconfig.Layer{
					Name:   string(LayerProject),
					Source: sdkconfig.LiteralSource(string(data)),
				})
				path = projectFile
			}
		}
		layered, err := m.loader.LoadLayers(ctx, layers)
		if err != nil {
			return nil, fmt.Errorf("config %s.yaml: %w", name, err)
		}
		v.Raw[name+".yaml"] = layered.Data
		v.Paths[name+".yaml"] = path
		for key, origin := range layered.Origins {
			v.Origins[name+".yaml."+key] = origin
		}
		switch name {
		case "tools":
			doc, err := toolconfig.Parse(layered.Data)
			if err != nil {
				return nil, fmt.Errorf("tools.yaml: %w", err)
			}
			v.Tools = &doc
		case "sandbox":
			doc, err := sandboxconfig.Parse(layered.Data)
			if err != nil {
				return nil, fmt.Errorf("sandbox.yaml: %w", err)
			}
			v.Sandbox = &doc
		}
	}
	return v, nil
}

// Validate loads and strictly parses every document, failing on the
// first error.
func (m *Manager) Validate(ctx context.Context) error {
	_, err := m.Load(ctx)
	return err
}

// Update merges a partial patch into the target layer's document file
// and writes it back. Patch semantics match LoadLayers: maps merge
// recursively, scalars and arrays are replaced wholesale, and an
// explicit null deletes a key. The merged document is strictly parsed
// before anything is written. When layer is the project layer and no
// project directory exists yet, it is created under the work directory.
func (m *Manager) Update(
	ctx context.Context,
	docName string,
	layer Layer,
	patch any,
) error {
	if !validDocument(docName) {
		return fmt.Errorf("config: unknown document %q", docName)
	}
	if layer != LayerUser && layer != LayerProject {
		return fmt.Errorf("config: unknown layer %q", layer)
	}
	patchMap, ok := patch.(map[string]any)
	if !ok {
		return fmt.Errorf("config: patch must be a document object")
	}
	if _, has := patchMap["version"]; !has {
		patchMap["version"] = "v1"
	}
	dir := m.userDir
	if layer == LayerProject {
		dir = m.projectDir
		if dir == "" {
			dir = filepath.Join(m.workDir, ".opencraft", "config")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			m.projectDir = dir
		}
	}

	patchData, err := json.Marshal(patchMap)
	if err != nil {
		return fmt.Errorf("config: encode patch: %w", err)
	}
	layers := []sdkconfig.Layer{
		{Name: "patch", Source: sdkconfig.LiteralSource(string(patchData))},
	}
	target := filepath.Join(dir, docName+".yaml")
	if existing, err := os.ReadFile(target); err == nil {
		layers = append([]sdkconfig.Layer{
			{Name: "existing", Source: sdkconfig.LiteralSource(string(existing))},
		}, layers...)
	} else if !os.IsNotExist(err) {
		return err
	}
	merged, err := m.loader.LoadLayers(ctx, layers)
	if err != nil {
		return fmt.Errorf("config %s: %w", docName, err)
	}
	out, err := yaml.JSONToYAML(merged.Data)
	if err != nil {
		return fmt.Errorf("config %s: encode yaml: %w", docName, err)
	}
	previous, _ := os.ReadFile(target)
	if err := os.WriteFile(target, out, 0o600); err != nil {
		return err
	}
	// A project layer may be partial (e.g. only defaults), so validation
	// must run against the merged view (user layer + project layer), not
	// the file alone. Roll back on failure.
	if _, err := m.Load(ctx); err != nil {
		if previous == nil {
			_ = os.Remove(target)
		} else {
			_ = os.WriteFile(target, previous, 0o600)
		}
		return fmt.Errorf("config %s: %w", docName, err)
	}
	return nil
}

func validDocument(docName string) bool {
	switch docName {
	case "inference", "workspace", "tools", "sandbox":
		return true
	default:
		return false
	}
}
