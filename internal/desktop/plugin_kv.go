package desktop

// Plugin KV is the generic per-plugin storage capability: every plugin
// gets its own namespaced key/value store under
// <pluginsRoot>/.data/<id>/kv.json (0600). Values are the plugin's own
// non-secret data (UI prefs, cached lists); secret material must go
// through the auth/secret capabilities, which never return plaintext
// to JS.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const (
	// kvMaxValueBytes bounds one stored value so a plugin cannot grow
	// the state file unboundedly.
	kvMaxValueBytes = 64 << 10
	// kvFileName is the per-plugin state file inside .data/<id>/.
	kvFileName = "kv.json"
)

// kvKeyRe allows letters, digits, dot, underscore and dash, bounded to
// 128 chars and never starting with a dot.
var kvKeyRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// PluginKVEntry is one key/value pair in a plugin's storage.
type PluginKVEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// PluginKVGet returns one entry; Found is false when the key is absent.
func (a *App) PluginKVGet(pluginID, key string) (PluginKVEntry, error) {
	if err := validateKVKey(key); err != nil {
		return PluginKVEntry{}, err
	}
	store, err := a.readPluginKV(pluginID)
	if err != nil {
		return PluginKVEntry{}, err
	}
	return PluginKVEntry{Key: key, Value: store[key]}, nil
}

// PluginKVList returns every stored entry.
func (a *App) PluginKVList(pluginID string) ([]PluginKVEntry, error) {
	store, err := a.readPluginKV(pluginID)
	if err != nil {
		return nil, err
	}
	out := make([]PluginKVEntry, 0, len(store))
	for k, v := range store {
		out = append(out, PluginKVEntry{Key: k, Value: v})
	}
	return out, nil
}

// PluginKVSet stores one value, creating the plugin's state file on
// first use.
func (a *App) PluginKVSet(pluginID, key, value string) error {
	if err := validateKVKey(key); err != nil {
		return err
	}
	if len(value) > kvMaxValueBytes {
		return fmt.Errorf("plugins: kv value exceeds %d bytes", kvMaxValueBytes)
	}
	a.kvMu.Lock()
	defer a.kvMu.Unlock()
	store, err := a.readPluginKV(pluginID)
	if err != nil {
		return err
	}
	store[key] = value
	return a.writePluginKV(pluginID, store)
}

// PluginKVDelete removes one entry; a missing key is not an error.
func (a *App) PluginKVDelete(pluginID, key string) error {
	if err := validateKVKey(key); err != nil {
		return err
	}
	a.kvMu.Lock()
	defer a.kvMu.Unlock()
	store, err := a.readPluginKV(pluginID)
	if err != nil {
		return err
	}
	delete(store, key)
	return a.writePluginKV(pluginID, store)
}

func validateKVKey(key string) error {
	if !kvKeyRe.MatchString(key) {
		return fmt.Errorf("plugins: invalid kv key %q", key)
	}
	return nil
}

func (a *App) kvPath(pluginID string) (string, error) {
	if !pluginIDRe.MatchString(pluginID) {
		return "", fmt.Errorf("plugins: invalid plugin id %q", pluginID)
	}
	root, err := a.pluginRoot()
	if err != nil {
		return "", err
	}
	if _, err := a.readManifest(root, pluginID); err != nil {
		return "", err
	}
	dir := filepath.Join(root, ".data", pluginID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("plugins: create kv dir: %w", err)
	}
	return filepath.Join(dir, kvFileName), nil
}

// readPluginKV loads the plugin's state file. Callers holding kvMu may
// call this directly; otherwise it is safe on its own.
func (a *App) readPluginKV(pluginID string) (map[string]string, error) {
	path, err := a.kvPath(pluginID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("plugins: read kv: %w", err)
	}
	store := map[string]string{}
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, fmt.Errorf("plugins: decode kv: %w", err)
	}
	return store, nil
}

func (a *App) writePluginKV(pluginID string, store map[string]string) error {
	path, err := a.kvPath(pluginID)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("plugins: write kv: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("plugins: commit kv: %w", err)
	}
	return nil
}

// removePluginKVData drops a plugin's state directory on uninstall.
func (a *App) removePluginKVData(pluginID string) {
	root, err := a.pluginRoot()
	if err != nil {
		return
	}
	_ = os.RemoveAll(filepath.Join(root, ".data", pluginID))
}
