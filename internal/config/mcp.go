package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// MCPServer describes one external MCP tool server attached through the
// settings page. It mirrors flowcraft's core/tool/mcp.ServerSpec for
// the fields the UI manages.
type MCPServer struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"` // stdio | http
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
}

// mcpLayer is the user-layer document WriteMCP generates: the MCP tool
// source plus the tools assembly dep that exposes its tools.
type mcpLayer struct {
	Version   string `json:"version"`
	Resources struct {
		ToolMCP struct {
			Kind     string `json:"kind"`
			Impl     string `json:"impl"`
			Settings struct {
				Servers []MCPServer `json:"servers"`
			} `json:"settings"`
		} `json:"tool.mcp"`
		Tools struct {
			Deps map[string]string `json:"deps"`
		} `json:"tools"`
	} `json:"resources"`
}

// LoadMCP reads the configured MCP servers from the user
// configuration layer. A missing layer or resource returns an empty
// list.
func LoadMCP(configDir string) ([]MCPServer, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "opencraft.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []MCPServer{}, nil
		}
		return nil, err
	}
	var doc struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("config: parse user config: %w", err)
	}
	raw, ok := doc.Resources["tool.mcp"]
	if !ok {
		return []MCPServer{}, nil
	}
	var source struct {
		Settings struct {
			Servers []MCPServer `json:"servers"`
		} `json:"settings"`
	}
	if err := yaml.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("config: parse tool.mcp: %w", err)
	}
	if source.Settings.Servers == nil {
		return []MCPServer{}, nil
	}
	return source.Settings.Servers, nil
}

// WriteMCP persists the MCP server list into the user configuration
// layer, merging over it so the inference wiring and other manual
// resources survive. An empty list keeps an empty source so the tools
// assembly dep stays consistent; the servers just expose nothing.
func WriteMCP(configDir string, servers []MCPServer) error {
	layer := mcpLayer{Version: "v1"}
	layer.Resources.ToolMCP.Kind = "tool.Source"
	layer.Resources.ToolMCP.Impl = "mcp"
	layer.Resources.ToolMCP.Settings.Servers = servers
	layer.Resources.Tools.Deps = map[string]string{"tool.mcp": "tool.mcp"}
	fresh, err := yaml.Marshal(layer)
	if err != nil {
		return fmt.Errorf("config: render mcp layer: %w", err)
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		map[string]bool{"tool.mcp": true},
		map[string]bool{"tools": true},
	)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("config: create config dir: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, "opencraft.yaml"), merged, 0o600,
	); err != nil {
		return fmt.Errorf("config: write opencraft.yaml: %w", err)
	}
	return nil
}
