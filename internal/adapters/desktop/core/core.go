// Package core owns the desktop internal services behind the Wails v3 UI
// shell. It is not a Wails binding surface: bindings in the sibling package
// adapt these services for the UI shell.
package core

import (
	"sync"

	petfeed "github.com/GizClaw/opencraft/internal/adapters/desktop/pet"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	octelemetry "github.com/GizClaw/opencraft/internal/capabilities/telemetry"
	"github.com/GizClaw/opencraft/internal/foundation/version"
)

// Core is the composition root of the desktop services. It only wires
// services together; domain state lives on the individual services below.
type Core struct {
	mu sync.Mutex

	Shell *Shell
	// Runtime is wired once host acquisition is available.
	Runtime      *Runtime
	Conversation *Conversation
	Plugin       *PluginService
	Prompt       *Prompt
	Git          *GitService
	// Pet is the desktop pet activity feed. It is attached as a Shell
	// event observer so every UI/automation event flows through it.
	Pet *petfeed.PetActivityFeed
	// Packs is the shared pet pack registry (builtin + plugin packs).
	Packs *petfeed.PackStore
	// Telemetry owns the OTel pipelines. The desktop shell installs it
	// after the composition root is built (see desktop.New), so plugin
	// telemetry requests are refused while it is nil.
	Telemetry *octelemetry.Pipeline
	// telemetryMu guards telemetryLast, the plugin sink the user switch
	// suspended (see telemetry.go).
	telemetryMu   sync.Mutex
	telemetryLast *rememberedPluginSink
	// pluginWrites tracks the runtime rebuilds triggered by capability
	// plugin inference writes, so a plugin that re-submits an unchanged
	// row set does not rebuild the runtime per row (see inference.go).
	pluginWrites pluginInferenceWrite
	// rebuildMu guards rebuildPending, the set of workspaces whose
	// deferred rebuild is already armed (see runtime_reload.go). Several
	// reloads can invalidate one workspace while it drains; one armed
	// replacement is enough, and arming one goroutine per invalidation
	// made them all assemble at once when the drain finished.
	rebuildMu      sync.Mutex
	rebuildPending map[string]struct{}
	// path holds the last process PATH resolution for the diagnostics
	// view (see path_report.go).
	path pathReport

	UserDir string
	DataDir string
	// AppHome is the shared content and credential root: keyring/,
	// plugins/, and everything the deploy document points at through
	// ${ocraft:APP_HOME}. It defaults to DataDir, so a process that
	// only knows one root keeps the single-root layout.
	AppHome string
	// Profile names the state-root profile this process runs as; empty
	// for the default one. It is display-only (see the diagnostics
	// report), the roots above are already resolved.
	Profile string
	WorkDir string
}

// Paths are the roots one desktop process works against. They come from
// config.ResolveLaunch, which is the only place that decides between a
// flag, an environment variable, a profile and a default.
type Paths struct {
	// UserDir is the configuration directory: opencraft.yaml,
	// desktop.json, hooks.json (<app home>/config by default).
	UserDir string
	// DataDir is the state root: workspaces, user.db, logs, audit,
	// cache. One GUI process per state root.
	DataDir string
	// AppHome is the content and credential root (keyring/, plugins/,
	// agents/, skills/). Empty follows DataDir.
	AppHome string
	// Profile is the profile name the state root belongs to, if any.
	Profile string
	// WorkDir is the startup workspace; empty until one is selected.
	WorkDir string
}

// NewCore builds the service composition root for a two-root launch:
// the configuration directory and the state root, with the app home
// following the state root.
func NewCore(userDir, dataDir, workDir string) *Core {
	return NewCoreWithPaths(Paths{
		UserDir: userDir,
		DataDir: dataDir,
		WorkDir: workDir,
	})
}

// NewCoreWithPaths builds the service composition root from resolved
// launch paths. userDir and dataDir are required; the app home follows
// dataDir when it is empty.
func NewCoreWithPaths(p Paths) *Core {
	if p.AppHome == "" {
		p.AppHome = p.DataDir
	}
	runtime := NewRuntime(p.DataDir, p.UserDir, p.AppHome)
	plugin := NewPluginService(p.AppHome, version.ServiceVersion)
	pet := petfeed.NewPetActivityFeed(AssistantAgentID)
	packs := petfeed.NewPackStore(petfeed.BuiltinAssistantPack())
	c := &Core{
		Shell:        NewShell(p.UserDir),
		Runtime:      runtime,
		Conversation: NewConversation(),
		Plugin:       plugin,
		Prompt:       NewPrompt(),
		Git:          NewGitService(),
		Pet:          pet,
		Packs:        packs,
		UserDir:      p.UserDir,
		DataDir:      p.DataDir,
		AppHome:      p.AppHome,
		Profile:      p.Profile,
		WorkDir:      p.WorkDir,
	}
	c.Shell.SetPetSink(func(typ string, data any) {
		pet.OnEvent(typ, data)
	})
	runtime.Manager().SetAgentPlugins(plugin.Store, plugin.Capability)
	runtime.Manager().SetAutomationHost(NewAutomationHost(runtime))
	runtime.Manager().SetPluginInstaller(NewPluginInstaller(c))
	plugin.Capability.SetOpenURL(c.Shell.OpenURL)
	defaultMode, defaultThink := c.Shell.SessionDefaults()
	c.Conversation.SetDefaults(
		sessions.Mode(defaultMode), defaultThink,
	)
	c.wirePluginInference()
	c.wirePluginSessionImport()
	c.wirePluginWorkspace()
	c.wirePluginTelemetry()
	return c
}

// ActivePack resolves the character the pet window renders: the
// preferred pack when it is still registered, the builtin otherwise.
// Falling back to the builtin keeps the pet drivable when a plugin
// character is uninstalled while it is selected.
func (c *Core) ActivePack() petfeed.Pack {
	if id := c.Shell.AssistantPetCharacter(); id != "" {
		if pack, ok := c.Packs.Get(id); ok {
			return pack
		}
	}
	if pack, ok := c.Packs.Get(petfeed.BuiltinAssistantPackID); ok {
		return pack
	}
	// Registry setups without the builtin (tests, mocks) still render
	// something; this mirrors the frontend's pickPack fallback so both
	// sides resolve the same character.
	if first := c.Packs.List(); len(first) > 0 {
		return first[0]
	}
	return petfeed.BuiltinAssistantPack()
}
