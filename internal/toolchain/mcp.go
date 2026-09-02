package toolchain

import (
	"context"
	"fmt"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/tool"
	coretoolmcp "github.com/GizClaw/flowcraft/core/tool/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPResourceImpl is the deploy impl id of OpenCraft's toolchain-aware
// MCP source. It wraps flowcraft's MCP source and resolves bare stdio
// commands through the toolchain manager before the transport is
// built.
const MCPResourceImpl = "opencraft/mcp"

// MCPServer mirrors config.MCPServer without importing internal/config
// (config imports toolchain for runtime_preference, so a config import
// here would be a cycle).
type MCPServer struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
}

// MCPSettings is the settings subtree of the tool.mcp resource.
type MCPSettings struct {
	Servers []MCPServer `json:"servers"`
}

// MCPFactory builds the toolchain-aware tool.mcp source.
type MCPFactory struct{}

var _ resource.Factory = MCPFactory{}

// Spec implements resource.Factory.
func (MCPFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: MCPResourceImpl,
		Deps: []resource.DepSpec{
			{Name: "toolchain", Type: ResourceKind, Required: false},
		},
	}
}

// New implements resource.Factory.
func (MCPFactory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[MCPSettings](ctx, in.Settings)
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft mcp: decode settings: %v", err)
	}
	var mgr *Manager
	if dep, ok := in.Dep("toolchain"); ok {
		var ok bool
		mgr, ok = dep.(*Manager)
		if !ok {
			return nil, errdefs.Validationf(
				"opencraft mcp: toolchain dep is %T, want *toolchain.Manager", dep)
		}
	}
	src := coretoolmcp.NewSource()
	for _, server := range settings.Servers {
		transport, err := mcpTransport(server, mgr)
		if err != nil {
			_ = src.Close()
			return nil, err
		}
		if err := src.AddServer(ctx, server.Name, transport); err != nil {
			_ = src.Close()
			return nil, fmt.Errorf(
				"opencraft mcp: attach server %q: %w", server.Name, err)
		}
	}
	return src, nil
}

// RegisterMCP adds the toolchain-aware MCP source factory.
func RegisterMCP(r *resource.Registry) error {
	return r.Register(MCPFactory{})
}

// mcpTransport builds one stdio/http transport. For stdio it resolves
// bare commands through the toolchain manager and attaches host env
// without overwriting explicit server env.
func mcpTransport(server MCPServer, mgr *Manager) (mcpsdk.Transport, error) {
	switch server.Transport {
	case "stdio":
		command := server.Command
		env := server.Env
		if mgr != nil {
			if resolved, err := mgr.ResolveMCPCommand(command); err == nil {
				command = resolved
			}
			env = mgr.AttachHostEnv(env)
		}
		if command == "" {
			return nil, fmt.Errorf(
				"opencraft mcp: server %q: stdio command is required",
				server.Name)
		}
		return coretoolmcp.Stdio(command, server.Args, env)
	case "http":
		return coretoolmcp.StreamableHTTP(server.URL, nil, nil)
	default:
		return nil, fmt.Errorf(
			"opencraft mcp: server %q: unknown transport %q",
			server.Name, server.Transport)
	}
}

var _ tool.Source = (*coretoolmcp.Source)(nil)
