package desktop

import (
	"errors"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
)

// PluginKVGet returns one plugin KV entry.
func (a *App) PluginKVGet(pluginID, key string) (plugins.KVEntry, error) {
	if a.kv == nil {
		return plugins.KVEntry{}, errors.New("plugin kv store is not ready")
	}
	return a.kv.Get(pluginID, key)
}

// PluginKVList returns every stored plugin KV entry.
func (a *App) PluginKVList(pluginID string) ([]plugins.KVEntry, error) {
	if a.kv == nil {
		return nil, errors.New("plugin kv store is not ready")
	}
	return a.kv.List(pluginID)
}

// PluginKVSet stores one plugin KV value.
func (a *App) PluginKVSet(pluginID, key, value string) error {
	if a.kv == nil {
		return errors.New("plugin kv store is not ready")
	}
	return a.kv.Set(pluginID, key, value)
}

// PluginKVDelete removes one plugin KV entry.
func (a *App) PluginKVDelete(pluginID, key string) error {
	if a.kv == nil {
		return errors.New("plugin kv store is not ready")
	}
	return a.kv.Delete(pluginID, key)
}
