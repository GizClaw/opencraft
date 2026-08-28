// Package secrets implements opencraft's credential store for
// flowcraft's declarative secret.Store resources, plus the app-side
// manager used by the settings page and the literal-key migration.
//
// One 0600 file per secret under a 0700 directory backs the store on
// every platform (the Linux approach): no keychain ACLs, no native
// authorization prompts, no cgo. Secrets are encrypted at rest with
// AES-256-GCM under a machine-local 32-byte key stored next to them as
// .key (0600). Pre-encryption plaintext files stay readable and are
// rewritten encrypted on the next Set. The resource impl id stays
// "keychain" and configs keep ${secret:keychain.<name>} references so
// existing user documents do not need rewriting. Deployments that need
// a richer backend (vault, 1Password, Secret Service) can register
// their own secret.Store impl without touching opencraft core.
package secrets

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/secret"
)

// ResourceImpl is the deploy impl id of this secret store. The id
// predates the file-only backend; it is kept so already-written
// ${secret:keychain.<name>} references keep resolving.
const ResourceImpl = "keychain"

// encMagic prefixes every sealed secret file so Get can distinguish
// ciphertext from legacy plaintext.
var encMagic = []byte("ocenc1:")

const (
	// encKeyLen is the AES-256 key size in bytes.
	encKeyLen = 32
	// encKeyFile is the machine-local key file inside the store dir.
	encKeyFile = ".key"
)

// DefaultService is the (retained) service name used for app
// credentials (inference keys today; SSO gateway tokens later). The
// file backend ignores it; it exists for config compatibility with the
// earlier Keychain backend.
const DefaultService = "opencraft"

// Store is the built secret.Store value: a credential backend plus the
// deployment flags (id / default) carried from settings.
// It implements resource.SecretStore for flowcraft's lazy resolution
// and exposes Set/Delete for the app-side manager.
type Store struct {
	backend backend
	id      string
	def     bool
}

// Settings is the settings subtree of the secret.Store/keychain
// resource.
type Settings struct {
	// ID is the short name used in ${secret:ID.NAME} references; empty
	// falls back to the deployment resource name.
	ID string `json:"id,omitempty"`
	// Default marks this store as the target of NAME-only
	// ${secret:NAME} references.
	Default bool `json:"default,omitempty"`
	// Service overrides the Keychain service name (default "opencraft").
	// Ignored by the file backend; kept for config compatibility.
	Service string `json:"service,omitempty"`
	// Dir is the credential store directory (0700). Required.
	Dir string `json:"dir,omitempty"`
}

// backend abstracts the OS credential storage.
type backend interface {
	Get(ctx context.Context, name string) (value string, found bool, err error)
	Set(ctx context.Context, name, value string) error
	Delete(ctx context.Context, name string) error
	Available() bool
}

// NewStore opens the 0600-file backend rooted at dir, creating the
// store directory (0700) and the AES key file (.key, 0600) on first
// use. service is retained for call-site compatibility but not used by
// the file backend. The returned store is usable even when the
// directory or key cannot be created: Lookup then reports an error,
// and Available reports false so callers can fall back to literal
// config storage.
func NewStore(dir, service string) (Store, error) {
	_ = service // file backend; kept for call-site compatibility.
	if strings.TrimSpace(dir) == "" {
		return Store{}, errors.New(
			"opencraft secrets: file backend requires settings.dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Store{}, fmt.Errorf(
			"opencraft secrets: create store dir: %w", err)
	}
	key, err := loadOrCreateKey(dir)
	if err != nil {
		return Store{}, err
	}
	return Store{backend: &fileBackend{dir: dir, key: key}}, nil
}

// Lookup implements resource.SecretStore.
func (s Store) Lookup(ctx context.Context, name string) (string, bool, error) {
	if s.backend == nil {
		return "", false, errors.New("opencraft secrets: store is unavailable")
	}
	return s.backend.Get(ctx, name)
}

// DefaultSecretStore implements resource.SecretStore.
func (s Store) DefaultSecretStore() bool { return s.def }

// SecretStoreID implements resource.SecretStoreID. Empty falls back to
// the deployment resource name.
func (s Store) SecretStoreID() string { return s.id }

// Available reports whether the OS credential backend can be used.
func (s Store) Available() bool {
	return s.backend != nil && s.backend.Available()
}

// Set stores one secret; Delete removes it.
func (s Store) Set(ctx context.Context, name, value string) error {
	if s.backend == nil {
		return errors.New("opencraft secrets: store is unavailable")
	}
	return s.backend.Set(ctx, name, value)
}

// Delete removes one secret. A missing item is not an error.
func (s Store) Delete(ctx context.Context, name string) error {
	if s.backend == nil {
		return errors.New("opencraft secrets: store is unavailable")
	}
	return s.backend.Delete(ctx, name)
}

// factory builds the secret.Store/keychain resource.
type factory struct{}

// Spec implements resource.Factory.
func (factory) Spec() resource.Spec {
	return resource.Spec{Kind: secret.ResourceKind, Impl: ResourceImpl}
}

// New implements resource.Factory.
func (factory) New(ctx context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[Settings](ctx, in.Settings)
	if err != nil {
		return nil, fmt.Errorf("opencraft secrets: decode settings: %w", err)
	}
	store, err := NewStore(settings.Dir, settings.Service)
	if err != nil {
		return nil, err
	}
	store.id = settings.ID
	store.def = settings.Default
	return store, nil
}

// Register adds the secret.Store/keychain factory to r.
func Register(r *resource.Registry) error {
	return r.Register(factory{})
}

// Manager is the app-side credential handle sharing the same backend as
// the deploy resource. It never fails to construct: an unavailable
// backend surfaces through Available and per-call errors.
type Manager struct {
	store Store
}

// NewManager returns a manager rooted at dir (the 0600-file backend).
// service is retained for call-site compatibility.
func NewManager(dir, service string) *Manager {
	store, err := NewStore(dir, service)
	if err != nil {
		// Unavailable store: NewStore only fails when the directory
		// cannot be created or the platform has no backend. Keep a
		// zero store so callers degrade gracefully.
		return &Manager{}
	}
	return &Manager{store: store}
}

// NewFileManager returns a manager backed by encrypted 0600 files
// under dir (creating the directory and key on first use). It exists
// for tests and headless deployments that want deterministic file
// storage. An unusable directory or key yields an unavailable manager.
func NewFileManager(dir string) *Manager {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return &Manager{}
	}
	key, err := loadOrCreateKey(dir)
	if err != nil {
		return &Manager{}
	}
	return &Manager{store: Store{backend: &fileBackend{dir: dir, key: key}}}
}

// Available reports whether the underlying backend is usable.
func (m *Manager) Available() bool {
	return m != nil && m.store.Available()
}

// Get returns one secret.
func (m *Manager) Get(ctx context.Context, account string) (string, bool, error) {
	if m == nil {
		return "", false, errors.New("opencraft secrets: manager is unavailable")
	}
	return m.store.Lookup(ctx, account)
}

// Set stores one secret.
func (m *Manager) Set(ctx context.Context, account, value string) error {
	if m == nil {
		return errors.New("opencraft secrets: manager is unavailable")
	}
	return m.store.Set(ctx, account, value)
}

// Delete removes one secret.
func (m *Manager) Delete(ctx context.Context, account string) error {
	if m == nil {
		return errors.New("opencraft secrets: manager is unavailable")
	}
	return m.store.Delete(ctx, account)
}

// AccountFor returns the credential account for one inference
// deployment id ("inference/deepseek-inst-abc"). Names are dot-free so
// they stay addressable as ${secret:keychain.<name>} references.
func AccountFor(deploymentID string) string {
	return "inference/" + deploymentID
}

// fileBackend stores one 0600 file per secret under a 0700 directory.
// File names are sha256 hashes of the account so arbitrary names can
// never escape the directory. With a key present every file is sealed
// with AES-256-GCM (magic + random nonce + ciphertext+tag); a nil key
// keeps the legacy plaintext behavior so tests and pre-encryption
// stores keep working.
type fileBackend struct {
	dir string
	key []byte
}

func (f *fileBackend) Available() bool { return f != nil && f.dir != "" }

func (f *fileBackend) path(name string) string {
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(f.dir, hex.EncodeToString(sum[:]))
}

func (f *fileBackend) Get(_ context.Context, name string) (string, bool, error) {
	raw, err := os.ReadFile(f.path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf(
			"opencraft secrets: read %q: %w", name, err)
	}
	if bytes.HasPrefix(raw, encMagic) {
		plain, err := f.decrypt(raw)
		if err != nil {
			return "", false, fmt.Errorf(
				"opencraft secrets: read %q: %w", name, err)
		}
		return strings.TrimRight(string(plain), "\r\n"), true, nil
	}
	// Legacy plaintext (written before encryption): readable, and the
	// next Set rewrites it sealed.
	return strings.TrimRight(string(raw), "\r\n"), true, nil
}

func (f *fileBackend) Set(_ context.Context, name, value string) error {
	data, err := f.seal([]byte(value))
	if err != nil {
		return fmt.Errorf("opencraft secrets: encrypt %q: %w", name, err)
	}
	if err := os.WriteFile(f.path(name), data, 0o600); err != nil {
		return fmt.Errorf("opencraft secrets: write %q: %w", name, err)
	}
	return nil
}

func (f *fileBackend) Delete(_ context.Context, name string) error {
	if err := os.Remove(f.path(name)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("opencraft secrets: delete %q: %w", name, err)
	}
	return nil
}

// seal encrypts value with AES-256-GCM under f.key. A nil key keeps
// the legacy plaintext format (tests and pre-encryption stores).
func (f *fileBackend) seal(value []byte) ([]byte, error) {
	if len(f.key) == 0 {
		return value, nil
	}
	gcm, err := newGCM(f.key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, value, nil)
	out := make([]byte, 0, len(encMagic)+len(nonce)+len(sealed))
	out = append(out, encMagic...)
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// decrypt reverses seal. Files without the magic prefix are legacy
// plaintext and are not routed here.
func (f *fileBackend) decrypt(raw []byte) ([]byte, error) {
	if len(f.key) == 0 {
		return nil, errors.New("opencraft secrets: no encryption key")
	}
	body := raw[len(encMagic):]
	gcm, err := newGCM(f.key)
	if err != nil {
		return nil, err
	}
	if len(body) < gcm.NonceSize() {
		return nil, errors.New("opencraft secrets: truncated sealed secret")
	}
	nonce, sealed := body[:gcm.NonceSize()], body[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("opencraft secrets: decrypt: %w", err)
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != encKeyLen {
		return nil, fmt.Errorf(
			"opencraft secrets: key must be %d bytes, got %d",
			encKeyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// loadOrCreateKey reads the store's 32-byte AES key, creating it with
// 0600 permissions on first use. Concurrent first runs race on O_EXCL;
// the loser reads the winner's key.
func loadOrCreateKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, encKeyFile)
	raw, err := os.ReadFile(path)
	if err == nil {
		return decodeKey(raw)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, encKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	fd, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil, rerr
			}
			return decodeKey(raw)
		}
		return nil, err
	}
	defer func() { _ = fd.Close() }()
	if err := fd.Chmod(0o600); err != nil {
		return nil, err
	}
	if _, err := fd.Write([]byte(hex.EncodeToString(key) + "\n")); err != nil {
		return nil, err
	}
	if err := fd.Sync(); err != nil {
		return nil, err
	}
	return key, nil
}

func decodeKey(raw []byte) ([]byte, error) {
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf(
			"opencraft secrets: decode key file: %w", err)
	}
	if len(key) != encKeyLen {
		return nil, fmt.Errorf(
			"opencraft secrets: key file must hold %d bytes, got %d",
			encKeyLen, len(key))
	}
	return key, nil
}
