// Package pluginagent adapts plugin krafts to the agent: kraft methods
// become ordinary tool.Tool values, and plugin-declared MCP servers
// are attached through the same flowcraft MCP source the settings page
// uses. Skills and hooks are consumed by their own resources
// (opencraft.skills / opencraft.hooks) through the shared plugin host.
package pluginagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/tool/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
)

// ResourceImpl is the deploy impl id of the plugin agent source.
const ResourceImpl = "opencraft/plugins"

// Aggregate bounds on what one assembly may publish from the whole
// registry. The per-plugin limits live in capabilities/plugins (the
// charter's agent.tool row names them); these cap their sum, because
// nothing there bounds how many plugins are installed. A registry of
// twenty plugins each at the per-plugin limit would otherwise hand the
// model 1280 tools and ~640 KiB of schemas in one round, and the
// deployment's per-round pools are finite (config/assets/tools.yaml):
// one oversized definition is skipped by the discovery pool, but a
// registry's worth of them starves the tools behind it.
const (
	// maxKraftTools bounds the kraft tools one assembly publishes,
	// across every plugin. It is four per-plugin full houses.
	maxKraftTools = 4 * 64
	// maxKraftDefinitionBytes bounds the sum of name, description and
	// input schema across those tools.
	maxKraftDefinitionBytes = 512 << 10 // 512 KiB
)

// PluginHost is the subset of the plugin host this source needs.
type PluginHost interface {
	ToolSpecs() []agent.ToolSpec
	MCPServers() []agent.MCPServer
	Invoke(
		ctx context.Context,
		pluginID, method string,
		args json.RawMessage,
	) (json.RawMessage, error)
	// Watch registers fn to run after every plugin registry mutation
	// and returns a cancel. The source republishes its kraft tools on
	// that signal, so a plugin installed or disabled while a turn runs
	// reaches the next round instead of the next assembly.
	Watch(fn func()) (cancel func())
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
	host, ok := dep.(PluginHost)
	if !ok || host == nil {
		return nil, errdefs.Validationf(
			"plugin agent tools: plugins dep is %T, want plugin host", dep)
	}
	return newSource(ctx, host)
}

// Source aggregates kraft tools and plugin MCP servers.
type Source struct {
	ctx        context.Context
	host       PluginHost
	mcpSources []*mcp.Source

	mu sync.Mutex
	// budgetReported records that this source already logged a
	// truncation, so a registry that stays over the aggregate bound
	// warns once per source instead of once per republish.
	budgetReported bool
	// kraftTools is the kraft set this source has published, keyed by
	// exposed tool name. registrar is the live registry the set is
	// published into, cancelled by cancel on Close; a nil registrar
	// means nothing published yet (Attach has not run), which is the
	// normal state while the assembly is still building.
	kraftTools map[string]tool.Tool
	registrar  tool.Registrar
	cancel     func()
}

func newSource(ctx context.Context, host PluginHost) (*Source, error) {
	kept, dropped := boundKraftSpecs(host.ToolSpecs())
	if dropped > 0 {
		warnKraftBudget(ctx, dropped, len(kept))
	}
	s := &Source{
		ctx:        ctx,
		host:       host,
		kraftTools: kraftToolSet(host, kept),
	}
	s.budgetReported = dropped > 0
	for _, server := range host.MCPServers() {
		src := mcp.NewSource()
		transport, err := serverTransport(server)
		if err != nil {
			telemetry.WarnErr(ctx,
				"plugin agent tools: close mcp source after transport failure",
				src.Close())
			telemetry.WarnErr(ctx,
				"plugin agent tools: close plugin sources after transport failure",
				s.Close())
			return nil, err
		}
		if err := src.AddServer(
			ctx, server.Name, transport, mcp.WithPrefix(server.Prefix),
		); err != nil {
			telemetry.WarnErr(ctx,
				"plugin agent tools: close mcp source after attach failure",
				src.Close())
			telemetry.WarnErr(ctx,
				"plugin agent tools: close plugin sources after attach failure",
				s.Close())
			return nil, fmt.Errorf(
				"plugin agent tools: attach mcp server %q (%s): %w",
				server.Name, server.PluginID, err)
		}
		s.mcpSources = append(s.mcpSources, src)
	}
	return s, nil
}

// kraftToolSet builds the adapters one manifest scan describes, keyed
// by the name each one is exposed under. specs are what
// boundKraftSpecs kept.
func kraftToolSet(host PluginHost, specs []agent.ToolSpec) map[string]tool.Tool {
	tools := make(map[string]tool.Tool, len(specs))
	for _, spec := range specs {
		t := &toolAdapter{host: host, spec: spec}
		tools[t.Definition().Name] = t
	}
	return tools
}

// boundKraftSpecs applies the aggregate bounds to one manifest scan and
// returns the specs that fit, in order, plus how many were left out.
// The order is the registry's (Store.Entries sorts by plugin id), so the
// same registry always keeps the same tools: the cap is not a race
// against plugin installation order.
func boundKraftSpecs(specs []agent.ToolSpec) ([]agent.ToolSpec, int) {
	kept := make([]agent.ToolSpec, 0, len(specs))
	bytes := 0
	for _, spec := range specs {
		size := len(spec.Name) + len(spec.Description) + len(spec.InputSchema)
		if len(kept) >= maxKraftTools {
			break
		}
		if bytes+size > maxKraftDefinitionBytes {
			break
		}
		bytes += size
		kept = append(kept, spec)
	}
	return kept, len(specs) - len(kept)
}

// warnKraftBudget reports one truncation: which bound was reached, how
// many tools were published and how many were dropped. The dropped tools
// are not lost forever — a deployment can raise the bound — but they are
// invisible to the model, so the line has to say so rather than leave a
// silently short tool list.
func warnKraftBudget(ctx context.Context, dropped, published int) {
	telemetry.Warn(ctx,
		"plugin agent tools: registry exceeds the aggregate tool budget; "+
			"the rest are not advertised to the model",
		otellog.Int("tools_published", published),
		otellog.Int("tools_dropped", dropped),
		otellog.Int("max_tools", maxKraftTools),
		otellog.Int("max_definition_bytes", maxKraftDefinitionBytes))
}

func serverTransport(s agent.MCPServer) (mcpsdk.Transport, error) {
	switch s.Transport {
	case "stdio":
		return mcp.Stdio(s.Command, s.Args, s.Env)
	case "http":
		return mcp.StreamableHTTP(s.URL, nil, nil)
	default:
		return nil, fmt.Errorf(
			"plugin agent tools: mcp server %q has unknown transport %q",
			s.Name, s.Transport)
	}
}

func (s *Source) Tools() []tool.Tool {
	s.mu.Lock()
	out := make([]tool.Tool, 0, len(s.kraftTools))
	for _, t := range s.kraftTools {
		out = append(out, t)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].Definition().Name < out[j].Definition().Name
	})
	for _, src := range s.mcpSources {
		out = append(out, src.Tools()...)
	}
	return out
}

func (s *Source) LazyTools() []tool.LazyTool { return nil }

// Attach implements tool.RegistryAttacher. The MCP sources use the
// registrar to publish their background connections; the kraft set uses
// it to republish when the plugin registry moves, so a plugin installed,
// updated or disabled mid-turn reaches the next round's definitions
// instead of the next assembly.
func (s *Source) Attach(r tool.Registrar) {
	for _, src := range s.mcpSources {
		src.Attach(r)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registrar = r
	if s.cancel == nil {
		s.cancel = s.host.Watch(s.republish)
	}
}

// republish reconciles the live registry with the current manifest
// scan: a tool the registry gained appears, one whose plugin was
// disabled, updated away or uninstalled goes. Definitions() is read once
// per round, so the model sees the change within the turn that caused
// it. MCP servers are not republished — they attach at assembly.
func (s *Source) republish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.registrar == nil {
		return
	}
	kept, dropped := boundKraftSpecs(s.host.ToolSpecs())
	if dropped > 0 && !s.budgetReported {
		s.budgetReported = true
		warnKraftBudget(s.ctx, dropped, len(kept))
	}
	next := kraftToolSet(s.host, kept)
	for name, t := range next {
		cur, published := s.kraftTools[name]
		if published && sameDefinition(cur, t) {
			continue
		}
		if published {
			// Same name, different tool: the registry keeps the first
			// definition it saw, so a changed schema or description has
			// to go through Remove before Add.
			s.registrar.Remove(name)
		}
		if err := s.registrar.Add(t); err != nil {
			telemetry.WarnErr(s.ctx,
				"plugin agent tools: publish tool after registry change failed",
				err, otellog.String("tool", name))
		}
	}
	for name := range s.kraftTools {
		if _, kept := next[name]; !kept {
			s.registrar.Remove(name)
		}
	}
	s.kraftTools = next
}

// sameDefinition reports whether two tools describe the same thing to
// the model and the executor. A spec change that moves neither (say a
// method nobody exposes) does not need a registry round trip.
func sameDefinition(a, b tool.Tool) bool {
	da, db := a.Definition(), b.Definition()
	if da.Name != db.Name || da.Description != db.Description ||
		!bytes.Equal(da.InputSchema, db.InputSchema) {
		return false
	}
	ma, aOK := a.(tool.ToolMetadata)
	mb, bOK := b.(tool.ToolMetadata)
	if aOK != bOK {
		return false
	}
	return !aOK || ma.Metadata() == mb.Metadata()
}

// Close releases the plugin MCP sources (krafts are
// owned by the plugin runtime manager and stop with the host).
func (s *Source) Close() error {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.registrar = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var first error
	for _, src := range s.mcpSources {
		if err := src.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// toolAdapter forwards one agent tool call to a kraft method.
type toolAdapter struct {
	host PluginHost
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

// Execute implements tool.Tool. The tool result is a single text part;
// plugin agent tools have no multimodal output.
func (a *toolAdapter) Execute(
	ctx context.Context,
	arguments string,
) (message.Content, error) {
	out, err := a.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (a *toolAdapter) execute(
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
