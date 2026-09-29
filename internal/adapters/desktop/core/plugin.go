package core

import (
	"path/filepath"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins/kraft"
	"github.com/GizClaw/opencraft/internal/capabilities/secrets"
)

// PluginService owns plugin registry, KV, kraft subprocesses and
// the credential manager.
type PluginService struct {
	Store   *plugins.Store
	KV      *plugins.KVStore
	Kraft   *kraft.Manager
	Secrets *secrets.Manager
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
	mgr := kraft.NewManager(
		pluginDir,
		kraft.DefaultLoader{
			Root: pluginDir,
			KraftFunc: func(id string) (kraft.Kraft, bool, error) {
				return store.Kraft(id)
			},
			DirFunc:         store.Dir,
			PermissionsFunc: store.Permissions,
		},
		sec,
	)
	mgr.SetHostVersion(version)
	return &PluginService{
		Store:   store,
		KV:      plugins.NewKVStore(pluginDir),
		Kraft:   mgr,
		Secrets: sec,
	}
}

// Close stops all kraft subprocesses.
func (p *PluginService) Close() {
	if p.Kraft != nil {
		p.Kraft.Shutdown()
	}
}
