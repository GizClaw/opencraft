// Package core owns the desktopv2 internal services. It is not a Wails
// binding surface: bindings in the parent package adapt these services
// for the UI shell.
package core

import (
	"sync"

	"github.com/GizClaw/opencraft/internal/capabilities/telemetry"
)

// Core is the composition root of the desktopv2 services. It only
// wires services together; domain state lives on the individual
// services below.
type Core struct {
	mu sync.Mutex

	Shell *Shell
	// Runtime is wired once host acquisition is available.
	Runtime      *Runtime
	Conversation *Conversation
	Plugin       *PluginService
	Prompt       *Prompt

	UserDir string
	DataDir string
	WorkDir string
}

// NewCore builds the service composition root. userDir/dataDir are
// required; workDir may be empty until a workspace is selected.
func NewCore(userDir, dataDir, workDir string) *Core {
	runtime := NewRuntime(dataDir, userDir)
	plugin := NewPluginService(dataDir, telemetry.ServiceVersion)
	c := &Core{
		Shell:        NewShell(userDir),
		Runtime:      runtime,
		Conversation: NewConversation(),
		Plugin:       plugin,
		Prompt:       NewPrompt(),
		UserDir:      userDir,
		DataDir:      dataDir,
		WorkDir:      workDir,
	}
	runtime.SetAgentPlugins(plugin.Store, plugin.Capability)
	plugin.Capability.SetOpenURL(c.Shell.OpenURL)
	c.wirePluginInference()
	c.wirePluginSessionImport()
	c.wirePluginWorkspace()
	return c
}
