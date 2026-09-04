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

// NewPluginService creates the plugin service under dataDir.
func NewPluginService(dataDir, version string) *PluginService {
	pluginDir := filepath.Join(dataDir, "plugins")
	store := plugins.NewStore(pluginDir)
	store.SetHostVersion(version)
	sec := secrets.NewManager(filepath.Join(dataDir, "keyring"))
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
