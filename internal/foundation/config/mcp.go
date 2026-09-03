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
		ToolMCP *mcpSourceLayer `json:"tool.mcp,omitempty"`
		Tools   struct {
			Deps map[string]string `json:"deps"`
		} `json:"tools"`
	} `json:"resources"`
}

// mcpSourceLayer is the tool.mcp resource declaration WriteMCP emits
// when at least one server is configured.
type mcpSourceLayer struct {
	Kind     string `json:"kind"`
	Impl     string `json:"impl"`
	Settings struct {
		Servers []MCPServer `json:"servers"`
	} `json:"settings"`
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
// resources survive. An empty list removes the tool.mcp source and its
// tools assembly dep, preserving any other user-declared tools deps, so
// the merged document never declares an MCP source without servers.
func WriteMCP(configDir string, servers []MCPServer) error {
	layer := mcpLayer{Version: "v1"}
	replaceKeys := map[string]bool{"tool.mcp": true}
	mergeKeys := map[string]bool{}
	if len(servers) > 0 {
		src := &mcpSourceLayer{Kind: "tool.Source", Impl: "mcp"}
		src.Settings.Servers = servers
		layer.Resources.ToolMCP = src
		layer.Resources.Tools.Deps = map[string]string{"tool.mcp": "tool.mcp"}
		mergeKeys["tools"] = true
	} else {
		// Replace the user-layer tools resource with every existing dep
		// except tool.mcp, so clearing MCP never deletes unrelated deps.
		layer.Resources.Tools.Deps = userLayerToolsDepsWithoutMCP(
			filepath.Join(configDir, "opencraft.yaml"))
		replaceKeys["tools"] = true
	}
	fresh, err := yaml.Marshal(layer)
	if err != nil {
		return fmt.Errorf("config: render mcp layer: %w", err)
	}
	merged, err := mergeUserLayer(
		filepath.Join(configDir, "opencraft.yaml"),
		fresh,
		replaceKeys,
		mergeKeys,
		false, // MCP does not own provider resources; preserve them
	)
	if err != nil {
		return err
	}
	return writeFileAtomic(
		filepath.Join(configDir, "opencraft.yaml"),
		merged,
		0o600,
	)
}

// userLayerToolsDepsWithoutMCP reads the current user layer's tools deps
// and returns every dependency except tool.mcp. Missing layers return an
// empty mapping.
func userLayerToolsDepsWithoutMCP(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var doc struct {
		Resources map[string]struct {
			Deps map[string]string `json:"deps"`
		} `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return out
	}
	tools, ok := doc.Resources["tools"]
	if !ok {
		return out
	}
	for key, ref := range tools.Deps {
		if key != "tool.mcp" {
			out[key] = ref
		}
	}
	return out
}
