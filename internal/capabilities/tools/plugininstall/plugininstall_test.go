package plugininstall

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	"github.com/GizClaw/opencraft/internal/foundation/interact"
)

// fakeInstaller records every call and replays canned summaries.
type fakeInstaller struct {
	installed []string
	updated   [][2]string
	inspected []string

	inspect  plugins.PluginSummary
	inspectE error
	installE error
	updateE  error
	// reloadFail models a registry write that succeeded while the
	// runtime reload afterwards failed.
	reloadFail bool
	list       []plugins.PluginSummary
}

func (f *fakeInstaller) PluginsList(
	context.Context,
) ([]plugins.PluginSummary, error) {
	return f.list, nil
}

func (f *fakeInstaller) PluginInspect(
	_ context.Context, src string,
) (plugins.PluginSummary, error) {
	f.inspected = append(f.inspected, src)
	if f.inspectE != nil {
		return plugins.PluginSummary{}, f.inspectE
	}
	return f.inspect, nil
}

func (f *fakeInstaller) PluginInstall(
	_ context.Context, src string,
) (plugins.PluginSummary, error) {
	f.installed = append(f.installed, src)
	if f.installE != nil {
		return plugins.PluginSummary{}, f.installE
	}
	if f.reloadFail {
		return f.inspect, errors.New("runtime reload: boom")
	}
	return f.inspect, nil
}

func (f *fakeInstaller) PluginUpdate(
	_ context.Context, id, src string,
) (plugins.PluginSummary, error) {
	f.updated = append(f.updated, [2]string{id, src})
	if f.updateE != nil {
		return plugins.PluginSummary{}, f.updateE
	}
	return f.inspect, nil
}

// fakeRoot resolves workspace-relative sources under one root.
type fakeRoot struct{ root string }

func (r fakeRoot) AbsPath(_ context.Context, path string) (string, error) {
	if path == "" {
		return "", errors.New("empty path")
	}
	return filepath.Join(r.root, path), nil
}

// promptHost answers the confirmation prompt with choice.
func promptHost(choice string, asks *[]string) context.Context {
	return agent.ContextWithHost(context.Background(), agent.HostFuncs{
		AskUserFn: func(
			_ context.Context, prompt agent.UserPrompt,
		) (agent.UserReply, error) {
			if asks != nil {
				for _, part := range prompt.Parts {
					if text, ok := part.(message.TextPart); ok {
						*asks = append(*asks, text.Text)
					}
				}
			}
			return agent.UserReply{
				Metadata: map[string]string{
					interact.MetaChoice: choice,
				},
			}, nil
		},
	})
}

func newTools(t *testing.T, inst Installer, root SourceRoot) []tool.Tool {
	t.Helper()
	return New(inst, root)
}

func callTool(
	t *testing.T, tools []tool.Tool, name, args string,
) (string, error) {
	t.Helper()
	return callToolCtx(t, context.Background(), tools, name, args)
}

// callToolCtx is callTool with an explicit context (for the
// confirmation host).
func callToolCtx(
	t *testing.T, ctx context.Context, tools []tool.Tool, name, args string,
) (string, error) {
	t.Helper()
	for _, tl := range tools {
		if tl.Definition().Name != name {
			continue
		}
		content, err := tl.Execute(ctx, args)
		if err != nil {
			return "", err
		}
		return content.Text(), nil
	}
	t.Fatalf("%s: tool not found", name)
	return "", nil
}

func summary() plugins.PluginSummary {
	return plugins.PluginSummary{
		ID:          "hello",
		Name:        "Hello Plugin",
		Version:     "0.1.0",
		Entry:       "dist/index.js",
		Capability:  "bin/hello",
		Permissions: []string{"storage:kv", "skills:contribute"},
		Enabled:     true,
		HasSkills:   true,
	}
}

// TestInstallConfirmsWithManifestFacts pins that the user sees what
// would run: permissions, entry bundle and capability binary.
func TestInstallConfirmsWithManifestFacts(t *testing.T) {
	inst := &fakeInstaller{inspect: summary()}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	var asks []string
	out, err := callToolCtx(t, promptHost("yes", &asks), tools,
		InstallName, `{"path":".opencraft-plugins/hello"}`)
	if err != nil {
		t.Fatalf("plugin_install: %v", err)
	}
	if len(inst.installed) != 1 ||
		inst.installed[0] != filepath.Join("/ws", ".opencraft-plugins/hello") {
		t.Fatalf("installed = %v, want the resolved workspace path", inst.installed)
	}
	if len(asks) != 1 {
		t.Fatalf("confirmation prompts = %d, want 1", len(asks))
	}
	for _, want := range []string{
		`Install plugin "hello"`, "Hello Plugin", "0.1.0", "storage:kv",
		"dist/index.js", "bin/hello", "skills",
	} {
		if !strings.Contains(asks[0], want) {
			t.Errorf("confirmation missing %q:\n%s", want, asks[0])
		}
	}
	var row pluginRow
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatalf("result is not JSON: %v (%q)", err, out)
	}
	if row.ID != "hello" || !row.Enabled || row.Note == "" {
		t.Fatalf("result row = %+v", row)
	}
}

// TestInstallDeniedChangesNothing pins the fail-closed path: a denied
// confirmation never reaches the registry.
func TestInstallDeniedChangesNothing(t *testing.T) {
	inst := &fakeInstaller{inspect: summary()}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	out, err := callToolCtx(t, promptHost("no", nil), tools,
		InstallName, `{"path":"plug"}`)
	if err != nil {
		t.Fatalf("plugin_install: %v", err)
	}
	if len(inst.installed) != 0 {
		t.Fatalf("denied install reached the registry: %v", inst.installed)
	}
	if !strings.Contains(out, "cancelled") {
		t.Fatalf("result = %q, want a cancelled marker", out)
	}
}

// TestInstallWithoutHostFailsClosed pins that a runtime with no user
// prompt cannot install anything.
func TestInstallWithoutHostFailsClosed(t *testing.T) {
	inst := &fakeInstaller{inspect: summary()}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	if _, err := callTool(t, tools, InstallName, `{"path":"plug"}`); err == nil {
		t.Fatal("install without a host must fail closed")
	}
	if len(inst.installed) != 0 {
		t.Fatalf("install ran without confirmation: %v", inst.installed)
	}
}

// TestInstallAskErrorFailsClosed pins the automation/headless shape: a
// runtime whose backend cannot ask the user (interact.Auto) fails the
// install instead of assuming consent.
func TestInstallAskErrorFailsClosed(t *testing.T) {
	inst := &fakeInstaller{inspect: summary()}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	ctx := agent.ContextWithHost(context.Background(), agent.HostFuncs{
		AskUserFn: func(
			context.Context, agent.UserPrompt,
		) (agent.UserReply, error) {
			return agent.UserReply{}, errors.New(
				"opencraft: no interactive user backend")
		},
	})
	if _, err := callToolCtx(t, ctx, tools,
		InstallName, `{"path":"plug"}`); err == nil {
		t.Fatal("install without an interactive user must fail")
	}
	if len(inst.installed) != 0 {
		t.Fatalf("install ran without an interactive user: %v", inst.installed)
	}
}

// TestInstallReportsReloadFailure pins that a plugin which reached the
// registry but failed to reload is reported as installed-with-warning
// instead of an error the model would retry into "already installed".
func TestInstallReportsReloadFailure(t *testing.T) {
	inst := &fakeInstaller{
		inspect:    summary(),
		reloadFail: true,
	}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	out, err := callToolCtx(t, promptHost("yes", nil), tools,
		InstallName, `{"path":"plug"}`)
	if err != nil {
		t.Fatalf("plugin_install: %v", err)
	}
	var row pluginRow
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatalf("result is not JSON: %v (%q)", err, out)
	}
	if row.ID != "hello" || !strings.Contains(row.Note, "boom") {
		t.Fatalf("result row = %+v, want the reload failure in note", row)
	}
}

// TestInstallRejectsUnknownArguments pins the strict argument decode.
func TestInstallRejectsUnknownArguments(t *testing.T) {
	inst := &fakeInstaller{inspect: summary()}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	if _, err := callToolCtx(t, promptHost("yes", nil), tools,
		InstallName, `{"path":"plug","force":true}`); err == nil {
		t.Fatal("unknown argument must fail")
	}
	if len(inst.installed) != 0 {
		t.Fatal("rejected call reached the registry")
	}
}

// TestUpdateConfirmsAndUpdates pins the update flow.
func TestUpdateConfirmsAndUpdates(t *testing.T) {
	inst := &fakeInstaller{inspect: summary()}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	out, err := callToolCtx(t, promptHost("yes", nil), tools,
		UpdateName, `{"id":"hello","path":"plug"}`)
	if err != nil {
		t.Fatalf("plugin_update: %v", err)
	}
	if len(inst.updated) != 1 || inst.updated[0][0] != "hello" ||
		inst.updated[0][1] != filepath.Join("/ws", "plug") {
		t.Fatalf("updated = %v", inst.updated)
	}
	if !strings.Contains(out, "replaced") {
		t.Fatalf("result = %q, want the update note", out)
	}
	if _, err := callToolCtx(t, promptHost("yes", nil), tools,
		UpdateName, `{"id":"","path":"plug"}`); err == nil {
		t.Fatal("empty plugin id must fail")
	}
}

// TestListReturnsRegistryRows pins the read-only listing.
func TestListReturnsRegistryRows(t *testing.T) {
	inst := &fakeInstaller{list: []plugins.PluginSummary{summary()}}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	out, err := callTool(t, tools, ListName, `{}`)
	if err != nil {
		t.Fatalf("plugin_list: %v", err)
	}
	var payload struct {
		Plugins []pluginRow `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("result is not JSON: %v (%q)", err, out)
	}
	if len(payload.Plugins) != 1 || payload.Plugins[0].ID != "hello" {
		t.Fatalf("payload = %+v", payload)
	}
}

// TestUpdateRejectsMismatchedID pins that a source for another plugin
// is rejected before the user is asked to confirm anything.
func TestUpdateRejectsMismatchedID(t *testing.T) {
	inst := &fakeInstaller{inspect: summary()}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	var asks []string
	if _, err := callToolCtx(t, promptHost("yes", &asks), tools,
		UpdateName, `{"id":"other","path":"plug"}`); err == nil {
		t.Fatal("mismatched source id must fail")
	}
	if len(asks) != 0 || len(inst.updated) != 0 {
		t.Fatalf("mismatched update prompted=%v updated=%v", asks, inst.updated)
	}
}

// TestEmptyInstallerContributesNoTools pins the headless/CLI shape:
// the deploy graph resolves but no plugin tools reach the model.
func TestEmptyInstallerContributesNoTools(t *testing.T) {
	if got := EmptyInstaller().(interface{ Empty() bool }).Empty(); !got {
		t.Fatal("EmptyInstaller().Empty() = false, want true")
	}
	if _, err := EmptyInstaller().PluginsList(context.Background()); err == nil {
		t.Fatal("empty installer must report not-available")
	}
}

// TestInspectFailureStopsBeforeConfirm pins that an invalid source
// never reaches the confirmation.
func TestInspectFailureStopsBeforeConfirm(t *testing.T) {
	inst := &fakeInstaller{inspectE: errors.New("plugins: manifest requires name")}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	var asks []string
	if _, err := callToolCtx(t, promptHost("yes", &asks), tools,
		InstallName, `{"path":"plug"}`); err == nil {
		t.Fatal("invalid source must fail")
	}
	if len(asks) != 0 {
		t.Fatalf("prompted for an invalid source: %v", asks)
	}
}

// TestInstallRequiresPath pins the empty-path validation.
func TestInstallRequiresPath(t *testing.T) {
	inst := &fakeInstaller{inspect: summary()}
	tools := newTools(t, inst, fakeRoot{root: "/ws"})
	if _, err := callToolCtx(t, promptHost("yes", nil), tools,
		InstallName, `{}`); err == nil {
		t.Fatal("missing path must fail")
	}
}
