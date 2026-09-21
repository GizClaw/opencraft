package core

import (
	"path/filepath"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	"github.com/GizClaw/opencraft/internal/capabilities/secrets"
)

// PluginService owns plugin registry, KV, capability subprocesses and
// the credential manager.
type PluginService struct {
	Store      *plugins.Store
	KV         *plugins.KVStore
	Capability *pluginruntime.Manager
	Secrets    *secrets.Manager
}

// NewPluginService creates the plugin service under appHome: the
// registry, its KV store and the credential manager are user content
// shared by every instance of the same user, so a dev copy sees the
// plugins and secrets of the installed app instead of an empty shell.
// Keeping them off the state root is what makes "same user, another
// state root" work.
func NewPluginService(appHome, version string) *PluginService {
	pluginDir := filepath.Join(appHome, "plugins")
	store := plugins.NewStore(pluginDir)
	store.SetHostVersion(version)
	sec := secrets.NewManager(filepath.Join(appHome, "keyring"))
	cap := pluginruntime.NewManager(
		pluginDir,
		pluginruntime.DefaultLoader{
			Root: pluginDir,
			CapabilityFunc: func(id string) (pluginruntime.Capability, bool, error) {
				return store.Capability(id)
			},
			DirFunc: store.Dir,
		},
		sec,
	)
	cap.SetHostVersion(version)
	return &PluginService{
		Store:      store,
		KV:         plugins.NewKVStore(pluginDir),
		Capability: cap,
		Secrets:    sec,
	}
}

// Close stops all capability subprocesses.
func (p *PluginService) Close() {
	if p.Capability != nil {
		p.Capability.Shutdown()
	}
}
