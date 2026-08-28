// Package secrets implements opencraft's credential store for
// flowcraft's declarative secret.Store resources (impl "keychain"),
// plus the app-side manager used by the settings page and the
// literal-key migration.
//
// Backends are chosen per platform with zero cgo/build dependencies:
// macOS stores Generic Password items in the login Keychain through
// security(1); Linux stores one 0600 file per secret under a 0700
// directory. Deployments that need a richer backend (vault, 1Password,
// Secret Service) can register their own secret.Store impl without
// touching opencraft core.
package secrets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/secret"
)

// ResourceImpl is the deploy impl id of this secret store.
const ResourceImpl = "keychain"

// DefaultService is the Keychain service name used for app credentials
// (inference keys today; SSO gateway tokens later).
const DefaultService = "opencraft"

// Store is the built secret.Store value: a native credential backend
// plus the deployment flags (id / default) carried from settings.
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
	Service string `json:"service,omitempty"`
	// Dir is the Linux file-backend directory (0700). Required on
	// Linux; unused on macOS.
	Dir string `json:"dir,omitempty"`
}

// backend abstracts the OS credential storage.
type backend interface {
	Get(ctx context.Context, name string) (value string, found bool, err error)
	Set(ctx context.Context, name, value string) error
	Delete(ctx context.Context, name string) error
	Available() bool
}

// NewStore opens the native backend for the given service and (Linux)
// file directory. The returned store is usable even when the backend is
// unavailable: Lookup then reports an error, and Available reports
// false so callers can fall back to literal config storage.
func NewStore(dir, service string) (Store, error) {
	if service == "" {
		service = DefaultService
	}
	var b backend
	switch runtime.GOOS {
	case "darwin":
		b = &keychainBackend{service: service}
	case "linux":
		if strings.TrimSpace(dir) == "" {
			return Store{}, errors.New(
				"opencraft secrets: file backend requires settings.dir")
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Store{}, fmt.Errorf(
				"opencraft secrets: create store dir: %w", err)
		}
		b = &fileBackend{dir: dir}
	default:
		return Store{}, fmt.Errorf(
			"opencraft secrets: no backend for GOOS %q", runtime.GOOS)
	}
	return Store{backend: b}, nil
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

// NewManager returns a manager rooted at dir (Linux file backend) and
// service (macOS Keychain).
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

// NewFileManager returns a manager backed by 0600 files under dir,
// bypassing platform auto-detection. It exists for tests and for
// headless Linux deployments that want deterministic file storage
// regardless of a running Secret Service.
func NewFileManager(dir string) *Manager {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return &Manager{}
	}
	return &Manager{store: Store{backend: &fileBackend{dir: dir}}}
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

// keychainBackend stores Generic Password items through security(1).
// The service is the Keychain service; the name is the account.
type keychainBackend struct{ service string }

// securityTimeout bounds every security(1) invocation so a locked
// keychain or an unexpected interactive prompt can never hang the app.
const securityTimeout = 10 * time.Second

func (k *keychainBackend) Available() bool {
	_, err := exec.LookPath("security")
	return err == nil
}

func (k *keychainBackend) Get(ctx context.Context, name string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, securityTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx,
		"security", "find-generic-password",
		"-a", name, "-s", k.service, "-w").Output()
	if err != nil {
		// security(1) exits non-zero when the item is missing; the
		// credential contract surfaces that as not-found.
		return "", false, nil
	}
	return strings.TrimRight(string(out), "\r\n"), true, nil
}

func (k *keychainBackend) Set(ctx context.Context, name, value string) error {
	ctx, cancel := context.WithTimeout(ctx, securityTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx,
		"security", "add-generic-password",
		"-a", name, "-s", k.service, "-U", "-w")
	// security(1) reads the password interactively as two lines (value
	// + retype) even when -w has no argument; feed both so it never
	// falls back to a /dev/tty prompt (which would hang a GUI process).
	cmd.Stdin = strings.NewReader(value + "\n" + value + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("opencraft secrets: keychain add %q: %v: %s",
			name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (k *keychainBackend) Delete(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, securityTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx,
		"security", "delete-generic-password",
		"-a", name, "-s", k.service).CombinedOutput(); err != nil {
		return fmt.Errorf("opencraft secrets: keychain delete %q: %v: %s",
			name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// fileBackend stores one 0600 file per secret under a 0700 directory.
// File names are sha256 hashes of the account so arbitrary names can
// never escape the directory.
type fileBackend struct{ dir string }

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
	return strings.TrimRight(string(raw), "\r\n"), true, nil
}

func (f *fileBackend) Set(_ context.Context, name, value string) error {
	if err := os.WriteFile(f.path(name), []byte(value), 0o600); err != nil {
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
