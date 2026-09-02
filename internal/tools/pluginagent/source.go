// Package pluginagent exposes plugin-contributed capabilities to the
// agent: capability subprocess methods become ordinary tool.Tool
// values, and plugin-declared MCP servers are attached through the
// same flowcraft MCP source the settings page uses. Skills and hooks
// are consumed by their own resources (opencraft.skills /
// opencraft.hooks) through the shared plugin host.
package pluginagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/tool/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/GizClaw/opencraft/internal/plugins/agent"
	"github.com/GizClaw/opencraft/internal/toolchain"
)

// ResourceImpl is the deploy impl id of the plugin agent source.
const ResourceImpl = "opencraft/plugins"

// CapabilityHost is the subset of the plugin host this source needs.
type CapabilityHost interface {
	ToolSpecs() []agent.ToolSpec
	MCPServers() []agent.MCPServer
	Invoke(
		ctx context.Context,
		pluginID, method string,
		args json.RawMessage,
	) (json.RawMessage, error)
}

// SourceFactory builds the plugin agent tool source.
type SourceFactory struct{}

var _ resource.Factory = SourceFactory{}

// Spec implements resource.Factory.
func (SourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: ResourceImpl,
		Deps: []resource.DepSpec{
			{Name: "plugin.host", Type: agent.ResourceKind, Required: true},
			{Name: "toolchain", Type: toolchain.ResourceKind, Required: false},
		},
	}
}

// New implements resource.Factory. An empty plugin host contributes
// no tools.
func (SourceFactory) New(ctx context.Context, in resource.Input) (any, error) {
	dep, ok := in.Dep("plugin.host")
	if !ok {
		return nil, errdefs.Validationf(
			"plugin agent tools: plugins dependency is required")
	}
	host, ok := dep.(CapabilityHost)
	if !ok || host == nil {
		return nil, errdefs.Validationf(
			"plugin agent tools: plugins dep is %T, want capability host", dep)
	}
	var mgr *toolchain.Manager
	if dep, ok := in.Dep("toolchain"); ok {
		mgr, ok = dep.(*toolchain.Manager)
		if !ok {
			return nil, errdefs.Validationf(
				"plugin agent tools: toolchain dep is %T, want *toolchain.Manager",
				dep)
		}
	}
	return newSource(ctx, host, mgr)
}

// Source aggregates capability tools and plugin MCP servers.
type Source struct {
	host       CapabilityHost
	capTools   []tool.Tool
	mcpSources []*mcp.Source
}

func newSource(
	ctx context.Context,
	host CapabilityHost,
	managers ...*toolchain.Manager,
) (*Source, error) {
	var mgr *toolchain.Manager
	if len(managers) > 0 {
		mgr = managers[0]
	}
	s := &Source{host: host}
	for _, spec := range host.ToolSpecs() {
		s.capTools = append(s.capTools, &toolAdapter{host: host, spec: spec})
	}
	for _, server := range host.MCPServers() {
		src := mcp.NewSource()
		transport, err := serverTransport(server, mgr)
		if err != nil {
			_ = src.Close()
			_ = s.Close()
			return nil, err
		}
		if err := src.AddServer(
			ctx, server.Name, transport, mcp.WithPrefix(server.Prefix),
		); err != nil {
			_ = src.Close()
			_ = s.Close()
			return nil, fmt.Errorf(
				"plugin agent tools: attach mcp server %q (%s): %w",
				server.Name, server.PluginID, err)
		}
		s.mcpSources = append(s.mcpSources, src)
	}
	return s, nil
}

func serverTransport(
	s agent.MCPServer,
	mgr *toolchain.Manager,
) (mcpsdk.Transport, error) {
	switch s.Transport {
	case "stdio":
		command := s.Command
		env := s.Env
		if mgr != nil {
			if resolved, err := mgr.ResolveMCPCommand(command); err == nil {
				command = resolved
			}
			env = mgr.AttachHostEnv(env)
		}
		return mcp.Stdio(command, s.Args, env)
	case "http":
		return mcp.StreamableHTTP(s.URL, nil, nil)
	default:
		return nil, fmt.Errorf(
			"plugin agent tools: mcp server %q has unknown transport %q",
			s.Name, s.Transport)
	}
}

func (s *Source) Tools() []tool.Tool {
	out := append([]tool.Tool(nil), s.capTools...)
	for _, src := range s.mcpSources {
		out = append(out, src.Tools()...)
	}
	return out
}

func (s *Source) LazyTools() []tool.LazyTool { return nil }

// Attach implements tool.RegistryAttacher: capability tools are
// published immediately, and each MCP source receives the registrar so
// background connects can publish their tools.
func (s *Source) Attach(r tool.Registrar) {
	for _, t := range s.capTools {
		_ = r.Add(t) // duplicate of the construction-time snapshot is fine
	}
	for _, src := range s.mcpSources {
		src.Attach(r)
	}
}

// Close releases the plugin MCP sources (capability subprocesses are
// owned by the plugin runtime manager and stop with the host).
func (s *Source) Close() error {
	var first error
	for _, src := range s.mcpSources {
		if err := src.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// toolAdapter forwards one agent tool call to a capability method.
type toolAdapter struct {
	host CapabilityHost
	spec agent.ToolSpec
}

var (
	_ tool.Tool         = (*toolAdapter)(nil)
	_ tool.ToolMetadata = (*toolAdapter)(nil)
)

func (a *toolAdapter) Definition() message.ToolDefinition {
	// Provider tool-name schemas reject punctuation such as ':' and
	// '.', so namespace with '__' and sanitize dots in the plugin id.
	name := strings.ReplaceAll(a.spec.PluginID, ".", "_") +
		"__" + a.spec.Name
	description := a.spec.Description
	if description != "" {
		description = "[plugin " + a.spec.PluginID + "] " + description
	}
	return message.ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: normalizeSchema(a.spec.InputSchema),
	}
}

func (a *toolAdapter) Metadata() tool.ToolMeta {
	return tool.ToolMeta{
		MutatesState: a.spec.MutatesState,
		SelfTimeout:  true,
	}
}

func (a *toolAdapter) Execute(
	ctx context.Context,
	arguments string,
) (string, error) {
	args, err := decodeArguments(arguments)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "", errdefs.Validationf(
			"plugin agent tools: marshal arguments: %v", err)
	}
	result, err := a.host.Invoke(ctx, a.spec.PluginID, a.spec.Method, raw)
	if err != nil {
		return "", err
	}
	return renderResult(result), nil
}

func decodeArguments(arguments string) (map[string]any, error) {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return map[string]any{}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil, errdefs.Validationf(
			"plugin agent tools: parse arguments: %v", err)
	}
	if decoded == nil {
		return map[string]any{}, nil
	}
	return decoded, nil
}

func normalizeSchema(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return json.RawMessage(`{"type":"object"}`)
	}
	var probe map[string]any
	if json.Unmarshal([]byte(trimmed), &probe) != nil || probe == nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return json.RawMessage(trimmed)
}

// renderResult unquotes a JSON string result and passes everything
// else through verbatim.
func renderResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
