// Package plugininstall exposes plugin authoring to the agent: it
// installs and updates plugins from a workspace directory or zip
// through the desktop plugin registry, and lists what is installed.
//
// The registry lives under the user data dir, outside the session
// sandbox, so the copy, the manifest validation and the runtime
// reassembly all stay on the host; the tool only resolves the source
// path inside the workspace and asks the user to confirm before
// anything persists. Runtimes without a desktop registry (CLI /
// headless, tests) wire an empty installer and expose no tools.
package plugininstall

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/confirm"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

// Canonical tool names.
const (
	// ListName is the canonical plugin_list tool name.
	ListName = "plugin_list"
	// InstallName is the canonical plugin_install tool name.
	InstallName = "plugin_install"
	// UpdateName is the canonical plugin_update tool name.
	UpdateName = "plugin_update"
)

// ResourceKind is the deploy resource kind of the host-side plugin
// installer.
const ResourceKind = "opencraft.plugin_installer"

// ResourceImpl is the deploy impl id of the plugin install tool
// source.
const ResourceImpl = "opencraft/plugininstall"

// Installer is the host-side plugin registry surface the tools call.
// The registry owns every gate (manifest validation, host version,
// version ordering, builtin shadowing, zip-slip checks) and reloads
// the runtime after a mutation so the plugin's skills, tools, MCP
// servers and hooks go live.
type Installer interface {
	// PluginsList returns every installed plugin.
	PluginsList(ctx context.Context) ([]plugins.PluginSummary, error)
	// PluginInspect reads a source without installing it.
	PluginInspect(
		ctx context.Context, src string,
	) (plugins.PluginSummary, error)
	// PluginInstall installs a source as a new plugin.
	PluginInstall(
		ctx context.Context, src string,
	) (plugins.PluginSummary, error)
	// PluginUpdate replaces an installed plugin with a newer source.
	PluginUpdate(
		ctx context.Context, id, src string,
	) (plugins.PluginSummary, error)
}

// SourceRoot resolves a workspace-relative plugin source into the
// absolute host path the installer copies from. The deployment's
// hostworkspace resource implements it, so a source obeys exactly the
// same confinement rules as the file tools (workspace mode) and the
// same full-host reach as the file tools (YOLO mode).
type SourceRoot interface {
	AbsPath(ctx context.Context, path string) (string, error)
}

// Factory builds the opencraft.plugin_installer resource. A nil
// installer yields the empty one, so runtimes without a desktop
// registry still resolve the deploy graph.
type Factory struct {
	Installer Installer
}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: "local"}
}

// New implements resource.Factory.
func (f Factory) New(
	_ context.Context, _ resource.Input,
) (any, error) {
	if f.Installer == nil {
		return EmptyInstaller(), nil
	}
	return f.Installer, nil
}

// emptyInstaller reports that no desktop plugin registry is wired in.
type emptyInstaller struct{}

// EmptyInstaller returns the installer used by runtimes without a
// desktop plugin registry (CLI / headless, tests). It satisfies
// Installer and contributes no tools.
func EmptyInstaller() Installer { return emptyInstaller{} }

func (emptyInstaller) PluginsList(
	context.Context,
) ([]plugins.PluginSummary, error) {
	return nil, errdefs.NotAvailablef(
		"plugin install: no plugin registry in this runtime")
}

func (emptyInstaller) PluginInspect(
	context.Context, string,
) (plugins.PluginSummary, error) {
	return plugins.PluginSummary{}, errdefs.NotAvailablef(
		"plugin install: no plugin registry in this runtime")
}

func (emptyInstaller) PluginInstall(
	context.Context, string,
) (plugins.PluginSummary, error) {
	return plugins.PluginSummary{}, errdefs.NotAvailablef(
		"plugin install: no plugin registry in this runtime")
}

func (emptyInstaller) PluginUpdate(
	context.Context, string, string,
) (plugins.PluginSummary, error) {
	return plugins.PluginSummary{}, errdefs.NotAvailablef(
		"plugin install: no plugin registry in this runtime")
}

func (emptyInstaller) Empty() bool { return true }

// SourceFactory builds the plugin install tool source from the
// installer resource and the workspace path resolver.
type SourceFactory struct{}

var _ resource.Factory = SourceFactory{}

// Spec implements resource.Factory.
func (SourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: ResourceImpl,
		Deps: []resource.DepSpec{
			{
				Name:     "plugin.installer",
				Type:     ResourceKind,
				Required: true,
			},
			{
				Name:     "workspace",
				Type:     "opencraft.hostworkspace",
				Required: true,
			},
		},
	}
}

// New implements resource.Factory. An empty installer contributes no
// tools.
func (SourceFactory) New(
	_ context.Context, in resource.Input,
) (any, error) {
	installer, err := resourcedep.Required[Installer](
		in, "plugin install tools", "plugin.installer")
	if err != nil {
		return nil, err
	}
	root, err := resourcedep.Required[SourceRoot](
		in, "plugin install tools", "workspace")
	if err != nil {
		return nil, err
	}
	if empty, ok := installer.(interface{ Empty() bool }); ok && empty.Empty() {
		return source{}, nil
	}
	return source{tools: New(installer, root)}, nil
}

// source is the tool group contributed by one plugin installer.
type source struct {
	tools []tool.Tool
}

func (s source) Tools() []tool.Tool { return s.tools }

func (source) LazyTools() []tool.LazyTool { return nil }

var _ tool.Source = source{}

// Tool groups the plugin authoring tools over one registry and
// workspace resolver.
type Tool struct {
	installer Installer
	root      SourceRoot
}

// New creates the plugin authoring tools.
func New(installer Installer, root SourceRoot) []tool.Tool {
	t := &Tool{installer: installer, root: root}
	return []tool.Tool{
		listTool{t},
		installTool{t},
		updateTool{t},
	}
}

// ---------------------------------------------------------------------------
// plugin_list
// ---------------------------------------------------------------------------

type listTool struct{ t *Tool }

var _ tool.Tool = listTool{}

// Definition implements tool.Tool.
func (listTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		ListName,
		"Lists the plugins installed in OpenCraft (id, name, version, "+
			"enabled state, declared permissions and contributed "+
			"capabilities). Use it before installing to see what is "+
			"already there. Returns JSON.",
	).DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (listTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute implements tool.Tool.
func (t listTool) Execute(
	ctx context.Context, arguments string,
) (message.Content, error) {
	if err := strictDecode(arguments, nil); err != nil {
		return message.Content{}, err
	}
	list, err := t.t.installer.PluginsList(ctx)
	if err != nil {
		return message.Content{}, err
	}
	rows := make([]pluginRow, 0, len(list))
	for _, p := range list {
		rows = append(rows, rowFromSummary(p))
	}
	data, err := json.Marshal(map[string]any{"plugins": rows})
	if err != nil {
		return message.Content{}, errdefs.Internalf(
			"%s: marshal result: %v", ListName, err)
	}
	return message.NewTextContent(string(data)), nil
}

// ---------------------------------------------------------------------------
// plugin_install
// ---------------------------------------------------------------------------

type installTool struct{ t *Tool }

var _ tool.Tool = installTool{}

// Definition implements tool.Tool.
func (installTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		InstallName,
		"Installs a plugin into OpenCraft from a workspace directory "+
			"containing plugin.json or from a .zip package, then "+
			"reloads the runtime so its skills, tools, MCP servers and "+
			"hooks go live from the next turn on. The plugin is enabled "+
			"on install. The user reviews the manifest (id, version, "+
			"permissions, entry, capability binary) and confirms before "+
			"anything is copied; a denied install changes nothing. "+
			"Returns JSON.",
		message.ToolProperty("path", "string",
			"Plugin source, relative to the workspace root: a "+
				"directory containing plugin.json, or a .zip package."),
	).Required("path").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (installTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

// Execute implements tool.Tool.
func (t installTool) Execute(
	ctx context.Context, arguments string,
) (message.Content, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := strictDecode(arguments, &args, "path"); err != nil {
		return message.Content{}, err
	}
	src, candidate, err := t.t.source(ctx, args.Path)
	if err != nil {
		return message.Content{}, err
	}
	ok, err := confirm.Confirm(ctx, "Install plugin?",
		installPrompt(fmt.Sprintf("Install plugin %q (%s) v%s?",
			candidate.ID, candidate.Name, candidate.Version),
			candidate, src))
	if err != nil {
		return message.Content{}, err
	}
	if !ok {
		return message.NewTextContent(
			`{"cancelled":true,"action":"install"}`), nil
	}
	sum, err := t.t.installer.PluginInstall(ctx, src)
	return installResult(sum, err,
		"installed and enabled; the runtime reloaded, so the plugin's "+
			"skills, tools, MCP servers and hooks are live from the "+
			"next turn on")
}

// ---------------------------------------------------------------------------
// plugin_update
// ---------------------------------------------------------------------------

type updateTool struct{ t *Tool }

var _ tool.Tool = updateTool{}

// Definition implements tool.Tool.
func (updateTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		UpdateName,
		"Replaces an installed OpenCraft plugin with a newer source "+
			"from the workspace, then reloads the runtime. The source "+
			"manifest must keep the same id and carry a strictly newer "+
			"version; the previous version is kept as a rollback "+
			"snapshot, and enabled state, KV data, secrets and inference "+
			"profiles survive the update. The user confirms before "+
			"anything is replaced. Returns JSON.",
		message.ToolProperty("id", "string",
			"Plugin id to update, as installed (plugin_list)."),
		message.ToolProperty("path", "string",
			"Plugin source, relative to the workspace root: a "+
				"directory containing plugin.json, or a .zip package."),
	).Required("id", "path").DisallowAdditionalProperties().Build()
}

// Metadata implements tool.ToolMetadata.
func (updateTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

// Execute implements tool.Tool.
func (t updateTool) Execute(
	ctx context.Context, arguments string,
) (message.Content, error) {
	var args struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := strictDecode(arguments, &args, "id", "path"); err != nil {
		return message.Content{}, err
	}
	if strings.TrimSpace(args.ID) == "" {
		return message.Content{}, errdefs.Validationf(
			"%s: plugin id is required", UpdateName)
	}
	src, candidate, err := t.t.source(ctx, args.Path)
	if err != nil {
		return message.Content{}, err
	}
	if candidate.ID != args.ID {
		return message.Content{}, errdefs.Validationf(
			"%s: source manifest id %q does not match plugin %q",
			UpdateName, candidate.ID, args.ID)
	}
	ok, err := confirm.Confirm(ctx, "Update plugin?",
		installPrompt(fmt.Sprintf("Update plugin %q to v%s?",
			args.ID, candidate.Version), candidate, src))
	if err != nil {
		return message.Content{}, err
	}
	if !ok {
		return message.NewTextContent(
			`{"cancelled":true,"action":"update"}`), nil
	}
	sum, err := t.t.installer.PluginUpdate(ctx, args.ID, src)
	return installResult(sum, err,
		"replaced; the runtime reloaded, so the new version is live "+
			"from the next turn on")
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// source resolves a workspace-relative plugin source and reads its
// manifest so the confirmation can describe what would be installed.
func (t *Tool) source(
	ctx context.Context, path string,
) (string, plugins.PluginSummary, error) {
	if strings.TrimSpace(path) == "" {
		return "", plugins.PluginSummary{}, errdefs.Validationf(
			"plugin install: path is required")
	}
	if t.root == nil {
		return "", plugins.PluginSummary{}, errdefs.NotAvailablef(
			"plugin install: no workspace in this runtime")
	}
	src, err := t.root.AbsPath(ctx, path)
	if err != nil {
		return "", plugins.PluginSummary{}, err
	}
	sum, err := t.installer.PluginInspect(ctx, src)
	if err != nil {
		return "", plugins.PluginSummary{}, err
	}
	return src, sum, nil
}

// installPrompt renders the confirmation body: the user approves code
// that the app will load and run, so every fact the host has about the
// source is on screen.
func installPrompt(
	header string, sum plugins.PluginSummary, src string,
) string {
	var b strings.Builder
	b.WriteString(header + "\n\n")
	fmt.Fprintf(&b, "Source: %s\n", src)
	if sum.Entry != "" {
		fmt.Fprintf(&b, "Entry bundle: %s\n", sum.Entry)
	}
	if sum.Capability != "" {
		fmt.Fprintf(&b,
			"Capability binary: %s (a native process the app runs)\n",
			sum.Capability)
	}
	if len(sum.Permissions) > 0 {
		fmt.Fprintf(&b, "Permissions: %s\n",
			strings.Join(sum.Permissions, ", "))
	}
	if caps := capabilityList(sum); caps != "" {
		fmt.Fprintf(&b, "Agent capabilities: %s\n", caps)
	}
	if sum.ShadowsBuiltin {
		fmt.Fprintf(&b,
			"This replaces the built-in plugin of the same id (v%s).\n",
			sum.BuiltinVersion)
	}
	b.WriteString(
		"\nThe plugin is enabled on install: its bundle runs with the " +
			"app's privileges. Uninstall it from Settings → Plugins.")
	return b.String()
}

// capabilityList names the agent-facing surfaces the manifest
// declares.
func capabilityList(sum plugins.PluginSummary) string {
	var parts []string
	if sum.HasSkills {
		parts = append(parts, "skills")
	}
	if sum.HasTools {
		parts = append(parts, "tools")
	}
	if sum.HasMCP {
		parts = append(parts, "mcp servers")
	}
	if sum.HasHooks {
		parts = append(parts, "hooks")
	}
	return strings.Join(parts, ", ")
}

// installResult renders one install/update outcome with the caller's
// success note. A registry mutation that succeeded but failed to
// reload the runtime is reported as a success with a warning: retrying
// would only answer "already installed".
func installResult(
	sum plugins.PluginSummary, err error, note string,
) (message.Content, error) {
	if err != nil {
		if sum.ID == "" {
			return message.Content{}, err
		}
		row := rowFromSummary(sum)
		row.Note = fmt.Sprintf(
			"the plugin is on disk but reloading the runtime failed: %v; "+
				"restart OpenCraft (or toggle the plugin in Settings → "+
				"Plugins) before using it", err)
		return marshalRow(row)
	}
	row := rowFromSummary(sum)
	row.Note = note
	return marshalRow(row)
}

// pluginRow is the JSON view one tool returns per plugin.
type pluginRow struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Enabled        bool     `json:"enabled"`
	Builtin        bool     `json:"builtin,omitempty"`
	ShadowsBuiltin bool     `json:"shadows_builtin,omitempty"`
	Permissions    []string `json:"permissions,omitempty"`
	Entry          string   `json:"entry,omitempty"`
	Capability     string   `json:"capability,omitempty"`
	HasSkills      bool     `json:"has_skills,omitempty"`
	HasTools       bool     `json:"has_tools,omitempty"`
	HasMCP         bool     `json:"has_mcp,omitempty"`
	HasHooks       bool     `json:"has_hooks,omitempty"`
	Error          string   `json:"error,omitempty"`
	Note           string   `json:"note,omitempty"`
}

func rowFromSummary(sum plugins.PluginSummary) pluginRow {
	return pluginRow{
		ID:             sum.ID,
		Name:           sum.Name,
		Version:        sum.Version,
		Enabled:        sum.Enabled,
		Builtin:        sum.Builtin,
		ShadowsBuiltin: sum.ShadowsBuiltin,
		Permissions:    sum.Permissions,
		Entry:          sum.Entry,
		Capability:     sum.Capability,
		HasSkills:      sum.HasSkills,
		HasTools:       sum.HasTools,
		HasMCP:         sum.HasMCP,
		HasHooks:       sum.HasHooks,
		Error:          sum.Error,
	}
}

func marshalRow(row pluginRow) (message.Content, error) {
	data, err := json.Marshal(row)
	if err != nil {
		return message.Content{}, errdefs.Internalf(
			"plugin install: marshal result: %v", err)
	}
	return message.NewTextContent(string(data)), nil
}

// strictDecode rejects unknown argument keys so a mistyped field fails
// the call instead of silently installing something else. A nil v only
// validates the key set (tools that take no arguments).
func strictDecode(arguments string, v any, allowed ...string) error {
	if strings.TrimSpace(arguments) == "" {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &top); err != nil {
		return errdefs.Validationf("parse arguments: %v", err)
	}
	for key := range top {
		if !slices.Contains(allowed, key) {
			return errdefs.Validationf("unknown argument %q", key)
		}
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(arguments), v); err != nil {
		return errdefs.Validationf("parse arguments: %v", err)
	}
	return nil
}
