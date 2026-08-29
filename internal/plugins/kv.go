package plugins

// KVStore is the generic per-plugin storage capability: every plugin
// gets its own namespaced key/value store under
// <root>/.data/<id>/kv.json (0600). Values are the plugin's own
// non-secret data (UI prefs, cached lists); secret material must go
// through the auth/secret capabilities, which never return plaintext
// to JS.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

const (
	// MaxKVValueBytes bounds one stored value so a plugin cannot grow
	// the state file unboundedly.
	MaxKVValueBytes = 64 << 10
	// kvFileName is the per-plugin state file inside .data/<id>/.
	kvFileName = "kv.json"
)

// kvKeyRe allows letters, digits, dot, underscore and dash, bounded to
// 128 chars and never starting with a dot.
var kvKeyRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// KVEntry is one key/value pair in a plugin's storage.
type KVEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// KVStore is the per-plugin KV registry. Its methods serialize on one
// mutex so concurrent frontend calls cannot corrupt the state file.
type KVStore struct {
	root string
	mu   sync.Mutex
}

// NewKVStore returns a KV store rooted under the plugin registry root.
func NewKVStore(root string) *KVStore {
	return &KVStore{root: root}
}

// Get returns one entry; Value is empty when the key is absent.
func (s *KVStore) Get(pluginID, key string) (KVEntry, error) {
	if err := ValidateKVKey(key); err != nil {
		return KVEntry{}, err
	}
	store, err := s.read(pluginID)
	if err != nil {
		return KVEntry{}, err
	}
	return KVEntry{Key: key, Value: store[key]}, nil
}

// List returns every stored entry.
func (s *KVStore) List(pluginID string) ([]KVEntry, error) {
	store, err := s.read(pluginID)
	if err != nil {
		return nil, err
	}
	out := make([]KVEntry, 0, len(store))
	for k, v := range store {
		out = append(out, KVEntry{Key: k, Value: v})
	}
	return out, nil
}

// Set stores one value, creating the plugin's state file on first use.
func (s *KVStore) Set(pluginID, key, value string) error {
	if err := ValidateKVKey(key); err != nil {
		return err
	}
	if len(value) > MaxKVValueBytes {
		return fmt.Errorf("plugins: kv value exceeds %d bytes", MaxKVValueBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.read(pluginID)
	if err != nil {
		return err
	}
	store[key] = value
	return s.write(pluginID, store)
}

// Delete removes one entry; a missing key is not an error.
func (s *KVStore) Delete(pluginID, key string) error {
	if err := ValidateKVKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.read(pluginID)
	if err != nil {
		return err
	}
	delete(store, key)
	return s.write(pluginID, store)
}

// RemoveAll drops a plugin's state directory (uninstall cleanup).
func (s *KVStore) RemoveAll(pluginID string) {
	if err := ValidateID(pluginID); err != nil {
		return
	}
	_ = os.RemoveAll(filepath.Join(s.root, ".data", pluginID))
}

// ValidateKVKey checks one plugin KV key.
func ValidateKVKey(key string) error {
	if !kvKeyRe.MatchString(key) {
		return fmt.Errorf("plugins: invalid kv key %q", key)
	}
	return nil
}

func (s *KVStore) path(pluginID string) (string, error) {
	if err := ValidateID(pluginID); err != nil {
		return "", err
	}
	// KV data is namespaced by plugin id under .data/<id>/ and is
	// independent of where the plugin lives (user-installed or app-
	// bundled builtin), so no registry existence check is performed
	// here; ValidateID already bounds the id.
	dir := filepath.Join(s.root, ".data", pluginID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("plugins: create kv dir: %w", err)
	}
	return filepath.Join(dir, kvFileName), nil
}

func (s *KVStore) read(pluginID string) (map[string]string, error) {
	path, err := s.path(pluginID)
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

func (s *KVStore) write(pluginID string, store map[string]string) error {
	path, err := s.path(pluginID)
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
