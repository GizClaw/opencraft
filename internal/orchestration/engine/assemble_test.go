package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	sdkdelegation "github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/agents"
	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/hooks"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginagent "github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	plugininstalltool "github.com/GizClaw/opencraft/internal/capabilities/tools/plugininstall"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/interact"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
	"github.com/GizClaw/opencraft/internal/testing/sessionstore"
)

// automationStub is a non-empty host so the engine assembles the agent's
// scheduled-task tool without requiring a real user database.
type automationStub struct{}

func (automationStub) AutomationsList(context.Context) ([]automations.Task, error) {
	return nil, nil
}
func (automationStub) AutomationsGet(
	context.Context, string,
) (automations.Task, error) {
	return automations.Task{}, nil
}
func (automationStub) AutomationsPreview(
	context.Context, string, automations.Task,
) (automations.Task, error) {
	return automations.Task{}, nil
}
func (automationStub) AutomationsApply(
	context.Context, string, automations.Task,
) (automations.Task, error) {
	return automations.Task{}, nil
}

// recordingInstaller is a non-empty installer so the engine assembles
// the agent's plugin authoring tools without a desktop registry, while
// recording the host paths the tools resolve.
type recordingInstaller struct {
	summary   plugins.PluginSummary
	installed []string
	updated   [][2]string
}

func (r *recordingInstaller) PluginsList(
	context.Context,
) ([]plugins.PluginSummary, error) {
	return nil, nil
}

func (r *recordingInstaller) PluginInspect(
	context.Context, string,
) (plugins.PluginSummary, error) {
	return r.summary, nil
}

func (r *recordingInstaller) PluginInstall(
	_ context.Context, src string,
) (plugins.PluginSummary, error) {
	r.installed = append(r.installed, src)
	return r.summary, nil
}

func (r *recordingInstaller) PluginUpdate(
	_ context.Context, id, src string,
) (plugins.PluginSummary, error) {
	r.updated = append(r.updated, [2]string{id, src})
	return r.summary, nil
}

// pluginConfirmCtx answers the tool confirmation prompt.
func pluginConfirmCtx(approve bool) context.Context {
	choice := "no"
	if approve {
		choice = "yes"
	}
	return agent.ContextWithHost(context.Background(), agent.HostFuncs{
		AskUserFn: func(
			context.Context, agent.UserPrompt,
		) (agent.UserReply, error) {
			return agent.UserReply{
				Metadata: map[string]string{
					interact.MetaChoice: choice,
				},
			}, nil
		},
	})
}

// catalogHasTool reports whether the assembled tool catalog carries a
// tool with that name.
func catalogHasTool(asm *tool.Assembly, name string) bool {
	for _, def := range asm.Catalog().Definitions() {
		if def.Name == name {
			return true
		}
	}
	return false
}

func testWorkspaceLayout(
	t *testing.T, home, work string,
) *config.WorkspaceLayout {
	t.Helper()
	layout, err := config.ResolveWorkspace(
		filepath.Join(home, ".opencraft"), work)
	if err != nil {
		t.Fatal(err)
	}
	return &layout
}

// seedLocalSandboxConfig pre-seeds the user layer with local
// (non-execd) sandboxing: the test binary cannot fork itself into
// execd mode, so engine tests use the in-process platform backend.
// WriteInference preserves this box override.
func seedLocalSandboxConfig(t *testing.T, configDir string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := []byte("version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(
		filepath.Join(configDir, "opencraft.yaml"), seed, 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func migratedSessionStore(
	t *testing.T, layout *config.WorkspaceLayout,
) *ocsessions.Store {
	t.Helper()
	store, err := sessionstore.Open(t, layout.SessionsDir, 40)
	if err != nil {
		t.Fatalf("open migrated sessions: %v", err)
	}
	return store
}

// TestBuildRuntimeAssemblesNewTools verifies that the embedded deploy
// document (with the new tool sources) assembles into a runtime. The
// inference wiring and the local-sandbox override live in the
// wizard-generated user layer.
func TestBuildRuntimeAssemblesNewTools(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	// First-run wizard output: DeepSeek keyed, provider wired into the
	// infer assembly.
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models:    []config.Model{{Name: "test-model"}},
		}},
	}
	if err := configseed.Write(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}

	mgr, err := config.Open(config.Options{
		UserDir: userDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	layout := testWorkspaceLayout(t, home, work)
	sessionStore := migratedSessionStore(t, layout)
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithAutomationHost(automationStub{}),
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithWorkspaceLayout(layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return sessionStore, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	defer func() { _ = rt.Close() }()

	// The deployed assistant carries the run-level wall clock: with the
	// graph's iteration guard lifted (build.max_iterations: 0), this is
	// what bounds a runaway turn across revise attempts.
	assistant, ok := rt.Agent("assistant")
	if !ok {
		t.Fatal("assistant agent missing")
	}
	if assistant.Policy.RunTimeout != "2h" {
		t.Errorf("assistant policy.run_timeout = %q, want 2h",
			assistant.Policy.RunTimeout)
	}

	// Delegation wiring: the service and kanban backend resources
	// build, and the deployed assistant is a delegate target (the
	// deploy builder binds the directory through DeploymentBinder, so
	// a successful build implies the bind succeeded).
	serviceValue, ok := rt.Resource("delegate")
	if !ok {
		t.Fatal("delegate resource missing")
	}
	service, ok := serviceValue.(sdkdelegation.Service)
	if !ok || service == nil {
		t.Fatal("delegate resource is not a delegation.Service")
	}
	backendValue, ok := rt.Resource("delegate.backend")
	if !ok {
		t.Fatal("delegate.backend resource missing")
	}
	backend, ok := backendValue.(sdkdelegation.AsyncBackend)
	if !ok || backend == nil {
		t.Fatal("delegate.backend resource is not a delegation.AsyncBackend")
	}
	if _, ok := rt.Agent("assistant"); !ok {
		t.Fatal("assistant agent not deployed as a delegation target")
	}

	// Persistent subagent wiring: the lifecycle resource builds and is
	// bound to the runtime, and a created agent immediately becomes a
	// delegation target (core v0.1.24 dynamic directory).
	lifecycleValue, ok := rt.Resource("agentlifecycle")
	if !ok {
		t.Fatal("agentlifecycle resource missing")
	}
	lifecycle, ok := lifecycleValue.(*agents.Lifecycle)
	if !ok || lifecycle == nil {
		t.Fatal("agentlifecycle is not a *agents.Lifecycle")
	}
	if _, err := lifecycle.Create(context.Background(),
		agents.NewSpec("researcher", "Reads and summarizes the codebase",
			`{"name":"researcher","entry":"llm","nodes":[{"id":"llm","type":"inference","config":{"system_prompt":"Read-only researcher."}}],"edges":[{"from":"llm","to":"__end__"}]}`),
	); err != nil {
		t.Fatalf("Create persistent agent: %v", err)
	}
	dirValue, ok := rt.Resource("delegate.directory")
	if !ok {
		t.Fatal("delegate.directory resource missing")
	}
	directory, ok := dirValue.(*sdkdelegation.LocalDirectory)
	if !ok || directory == nil {
		t.Fatal("delegate.directory is not a *delegation.LocalDirectory")
	}
	targets, err := directory.List(context.Background())
	if err != nil {
		t.Fatalf("directory.List: %v", err)
	}
	found := false
	for _, target := range targets {
		if target.ID == "researcher" {
			found = true
		}
	}
	if !found {
		t.Fatalf("created agent missing from delegation targets: %+v", targets)
	}

	// The wired automation host exposes the scheduled-task tool.
	toolsValue, ok := rt.Resource("tools")
	if !ok {
		t.Fatal("tools resource missing")
	}
	toolsAsm, ok := toolsValue.(*tool.Assembly)
	if !ok || toolsAsm == nil {
		t.Fatal("tools resource is not *tool.Assembly")
	}
	foundAutomation := false
	for _, def := range toolsAsm.Catalog().Definitions() {
		if def.Name == "automation" {
			foundAutomation = true
			break
		}
	}
	if !foundAutomation {
		t.Fatal("automation tool missing from tool catalog")
	}
	// No installer is wired here: the plugin authoring tools must stay
	// out of the catalog so a headless runtime cannot install plugins.
	if catalogHasTool(toolsAsm, plugininstalltool.InstallName) {
		t.Fatal("plugin install tool present without an installer")
	}
}

// TestBuildRuntimeWithPluginInstallerExposesTools verifies the desktop
// shape: an injected registry exposes plugin_list / plugin_install /
// plugin_update, which copy from the workspace through the host.
func TestBuildRuntimeWithPluginInstallerExposesTools(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models:    []config.Model{{Name: "test-model"}},
		}},
	}
	if err := configseed.Write(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}

	mgr, err := config.Open(config.Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	layout := testWorkspaceLayout(t, home, work)
	sessionStore := migratedSessionStore(t, layout)
	installer := &recordingInstaller{summary: plugins.PluginSummary{
		ID: "hello", Name: "Hello", Version: "0.1.0", Enabled: true,
	}}
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithPluginInstaller(installer),
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithWorkspaceLayout(layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return sessionStore, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime with plugin installer: %v", err)
	}
	defer func() { _ = rt.Close() }()

	toolsValue, ok := rt.Resource("tools")
	if !ok {
		t.Fatal("tools resource missing")
	}
	asm, ok := toolsValue.(*tool.Assembly)
	if !ok || asm == nil {
		t.Fatal("tools resource is not *tool.Assembly")
	}
	for _, name := range []string{
		plugininstalltool.ListName,
		plugininstalltool.InstallName,
		plugininstalltool.UpdateName,
	} {
		if !catalogHasTool(asm, name) {
			t.Fatalf("%s missing from tool catalog", name)
		}
	}

	// Drive plugin_install through the assembled dispatcher: the source
	// path must resolve against the real workspace resource, so a
	// workspace-relative path reaches the installer as an absolute host
	// path while escapes are rejected before the confirmation.
	work, err = filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	call := func(ctx context.Context, args string) message.ToolResult {
		t.Helper()
		return asm.Execute(ctx, message.ToolCall{
			ID:        "call-1",
			Name:      plugininstalltool.InstallName,
			Arguments: json.RawMessage(args),
		})
	}
	res := call(pluginConfirmCtx(true), `{"path":"plug"}`)
	if res.IsError {
		t.Fatalf("plugin_install(relative) failed: %s", res.Content.Text())
	}
	if len(installer.installed) != 1 ||
		installer.installed[0] != filepath.Join(work, "plug") {
		t.Fatalf("installed = %v, want the workspace-resolved path",
			installer.installed)
	}
	for _, args := range []string{
		`{"path":"../escape"}`, `{"path":"/etc"}`,
	} {
		if res := call(pluginConfirmCtx(true), args); !res.IsError {
			t.Fatalf("plugin_install(%s) succeeded, want rejection", args)
		}
	}
	if len(installer.installed) != 1 {
		t.Fatalf("rejected sources reached the installer: %v",
			installer.installed)
	}
	// No interactive user (automation / headless): fail closed.
	if res := call(context.Background(), `{"path":"plug"}`); !res.IsError {
		t.Fatal("plugin_install without a user backend must fail")
	}
	if len(installer.installed) != 1 {
		t.Fatalf("unconfirmed install reached the installer: %v",
			installer.installed)
	}
	// plugin_update takes the same workspace-relative source and only
	// reaches the registry after the id matches the manifest.
	res = asm.Execute(pluginConfirmCtx(true), message.ToolCall{
		ID:        "call-2",
		Name:      plugininstalltool.UpdateName,
		Arguments: json.RawMessage(`{"id":"hello","path":"plug"}`),
	})
	if res.IsError {
		t.Fatalf("plugin_update failed: %s", res.Content.Text())
	}
	if len(installer.updated) != 1 ||
		installer.updated[0] != [2]string{"hello", filepath.Join(work, "plug")} {
		t.Fatalf("updated = %v, want the workspace-resolved path",
			installer.updated)
	}
}

func TestBuildRuntimeWithPluginHostExposesAgentCapabilities(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models:    []config.Model{{Name: "test-model"}},
		}},
	}
	if err := configseed.Write(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}

	// A real plugin on disk: one skill, one hook file, one capability
	// tool declaration.
	pluginRoot := t.TempDir()
	plugDir := filepath.Join(pluginRoot, "plug")
	for _, dir := range []string{
		filepath.Join(plugDir, "dist"),
		filepath.Join(plugDir, "skills", "hello"),
		filepath.Join(plugDir, "hooks"),
		filepath.Join(plugDir, "bin"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := map[string]any{
		"id": "plug", "name": "Plug", "version": "0.1.0",
		"entry":      "dist/index.js",
		"capability": map[string]any{"binary": "bin/ping", "protocol": 1},
		"permissions": []string{
			"skills:contribute", "hooks:register", "tools:expose",
		},
		"skills": []string{"skills"},
		"hooks":  []string{"hooks/hooks.json"},
		"tools": []any{map[string]any{
			"name": "ping", "description": "Ping", "method": "ping",
		}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"plugin.json":   string(raw),
		"dist/index.js": "export const apply = () => {};",
		"skills/hello/SKILL.md": "---\nname: hello\n" +
			"description: Hello plugin skill\n---\nBody.\n",
		"hooks/hooks.json": `{
			"hooks": {
				"PreToolUse": [{"hooks": [{"command": "cat > plugin-hook.out"}]}]
			}
		}`,
		"bin/ping": "#!/bin/sh\nexit 0\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(plugDir, rel), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	host := pluginagent.NewHost(
		context.Background(), plugins.NewStore(pluginRoot), nil,
	)
	if roots := host.SkillRoots(); len(roots) != 1 {
		t.Fatalf("plugin host skill roots = %v, want the plugin skills dir", roots)
	}

	mgr, err := config.Open(config.Options{
		UserDir: userDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	layout := testWorkspaceLayout(t, home, work)
	sessionStore := migratedSessionStore(t, layout)
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithAgentPlugins(host),
		WithWorkspaceLayout(layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return sessionStore, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime with plugin host: %v", err)
	}
	defer func() { _ = rt.Close() }()

	// Plugin skill is part of the shared skills registry.
	skillsValue, ok := rt.Resource("skills")
	if !ok {
		t.Fatal("skills resource missing")
	}
	svc, ok := skillsValue.(*skills.Service)
	if !ok || svc == nil {
		t.Fatal("skills resource is not *skills.Service")
	}
	foundSkill := false
	for _, sk := range svc.List() {
		if sk.Name == "hello" {
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Fatalf("plugin skill not discovered: roots=%v scan=%v errors=%v",
			svc.Roots(), svc.Errors(), svc.List())
	}

	// Plugin hook is merged into the runtime hooks manager and runs
	// with the plugin directory as cwd.
	hooksValue, ok := rt.Resource("hooks")
	if !ok {
		t.Fatal("hooks resource missing")
	}
	hookMgr, ok := hooksValue.(*hooks.Manager)
	if !ok || hookMgr == nil || hookMgr.Empty() {
		t.Fatal("hooks resource is not a non-empty *hooks.Manager")
	}
	hookMgr.Fire(context.Background(), hooks.EventPreToolUse, map[string]any{
		"event": hooks.EventPreToolUse,
		"tool":  "exec_command",
	})
	if _, err := os.Stat(filepath.Join(plugDir, "plugin-hook.out")); err != nil {
		t.Fatalf("plugin hook did not run through the runtime: %v", err)
	}

	// Plugin capability tool is in the assembled tool catalog.
	toolsValue, ok := rt.Resource("tools")
	if !ok {
		t.Fatal("tools resource missing")
	}
	asm, ok := toolsValue.(*tool.Assembly)
	if !ok || asm == nil {
		t.Fatal("tools resource is not *tool.Assembly")
	}
	foundTool := false
	for _, def := range asm.Catalog().Definitions() {
		if def.Name == "plug__ping" {
			foundTool = true
			break
		}
	}
	if !foundTool {
		t.Fatal("plugin capability tool missing from tool catalog")
	}
}

// TestRetiredPathRefsReportAndRepair covers the 0.4.0 upgrade path: a
// hand-authored user layer can still name ${env:OPEN_CRAFT_*}, because
// that was the only spelling a pre-resolver build understood. Loading
// must fail with an error anchored to the file and the replacement
// instead of an unset environment variable raised from inside
// deployment, and the diagnostics repair must leave a layer that
// assembles again (issue #115).
func TestRetiredPathRefsReportAndRepair(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")
	// The report only covers references that cannot resolve, so a
	// developer shell exporting the retired name would mask the upgrade.
	if previous, set := os.LookupEnv("OPEN_CRAFT_WORKDIR"); set {
		if err := os.Unsetenv("OPEN_CRAFT_WORKDIR"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Setenv("OPEN_CRAFT_WORKDIR", previous); err != nil {
				t.Errorf("restore OPEN_CRAFT_WORKDIR: %v", err)
			}
		})
	}

	userDir := filepath.Join(home, ".opencraft", "config")
	seedLocalSandboxConfig(t, userDir)
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      config.Providers[0].ID,
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models:    []config.Model{{Name: "test-model"}},
		}},
	}
	if err := configseed.Write(userDir, cfg); err != nil {
		t.Fatalf("write inference config: %v", err)
	}
	// A block copied out of an older embedded document.
	path := config.UserLayerFile(userDir)
	stale, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stale = append(stale, []byte(
		"agents:\n  assistant:\n    prepare:\n"+
			"      - type: opencraft.media\n"+
			"        settings:\n"+
			"          work_dir: ${env:OPEN_CRAFT_WORKDIR}\n")...)
	if err := os.WriteFile(path, stale, 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := config.Open(config.Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mgr.Load(context.Background())
	if err == nil {
		t.Fatal("load succeeded with a retired reference in the layer")
	}
	for _, want := range []string{
		path, "${env:OPEN_CRAFT_WORKDIR}", "${ocraft:WORKDIR}",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("load error %q does not mention %q", err, want)
		}
	}

	repair, err := config.RepairRetiredRefs(userDir)
	if err != nil {
		t.Fatalf("RepairRetiredRefs: %v", err)
	}
	if len(repair.Removed) == 0 {
		t.Fatal("repair removed nothing")
	}

	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load after repair: %v", err)
	}
	layout := testWorkspaceLayout(t, home, work)
	sessionStore := migratedSessionStore(t, layout)
	rt, err := BuildRuntime(
		context.Background(),
		view.Document,
		WithAutomationHost(automationStub{}),
		WithWorkBase(work),
		WithConfigBase(userDir),
		WithWorkspaceLayout(layout),
		WithSessionStore(func(
			context.Context, string, int,
		) (*ocsessions.Store, error) {
			return sessionStore, nil
		}),
	)
	if err != nil {
		t.Fatalf("BuildRuntime after repair: %v", err)
	}
	defer func() { _ = rt.Close() }()
}
