// Package core owns the desktop internal services behind the Wails v3 UI
// shell. It is not a Wails binding surface: bindings in the sibling package
// adapt these services for the UI shell.
package core

import (
	"sync"

	petfeed "github.com/GizClaw/opencraft/internal/adapters/desktop/pet"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
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

	UserDir string
	DataDir string
	WorkDir string
}

// NewCore builds the service composition root. userDir/dataDir are
// required; workDir may be empty until a workspace is selected.
func NewCore(userDir, dataDir, workDir string) *Core {
	runtime := NewRuntime(dataDir, userDir)
	plugin := NewPluginService(dataDir, version.ServiceVersion)
	pet := petfeed.NewPetActivityFeed(AssistantAgentID)
	packs := petfeed.NewPackStore(petfeed.BuiltinAssistantPack())
	c := &Core{
		Shell:        NewShell(userDir),
		Runtime:      runtime,
		Conversation: NewConversation(),
		Plugin:       plugin,
		Prompt:       NewPrompt(),
		Git:          NewGitService(),
		Pet:          pet,
		Packs:        packs,
		UserDir:      userDir,
		DataDir:      dataDir,
		WorkDir:      workDir,
	}
	c.Shell.SetPetSink(func(typ string, data any) {
		pet.OnEvent(typ, data)
	})
	runtime.Manager().SetAgentPlugins(plugin.Store, plugin.Capability)
	runtime.Manager().SetAutomationHost(NewAutomationHost(runtime))
	plugin.Capability.SetOpenURL(c.Shell.OpenURL)
	defaultMode, defaultThink := c.Shell.SessionDefaults()
	c.Conversation.SetDefaults(
		sessions.Mode(defaultMode), defaultThink,
	)
	c.wirePluginInference()
	c.wirePluginSessionImport()
	c.wirePluginWorkspace()
	return c
}
