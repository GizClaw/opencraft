package secrets

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/secret"
)

func TestFileBackendRoundTrip(t *testing.T) {
	b := &fileBackend{dir: t.TempDir()}
	if err := b.Set("account-a", "value-a"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, found, err := b.Get("account-a")
	if err != nil || !found || got != "value-a" {
		t.Fatalf("Get = (%q, %v, %v), want value-a", got, found, err)
	}
	if _, found, err := b.Get("missing"); err != nil || found {
		t.Fatalf("Get(missing) = (%v, %v), want not found", found, err)
	}
	if err := b.Delete("account-a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := b.Get("account-a"); err != nil || found {
		t.Fatalf("Get after Delete = (%v, %v), want not found", found, err)
	}
}

func TestFileBackendNameCannotEscape(t *testing.T) {
	dir := t.TempDir()
	b := &fileBackend{dir: dir}
	// Names with separators and traversal attempts must stay inside the
	// store directory (file names are sha256 hashes).
	for _, name := range []string{"../outside", "a/b", `a\b`, ".."} {
		if err := b.Set(name, "v"); err != nil {
			t.Fatalf("Set(%q): %v", name, err)
		}
		got, found, err := b.Get(name)
		if err != nil || !found || got != "v" {
			t.Fatalf("Get(%q) = (%q, %v, %v)", name, got, found, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("store dir has %d entries, want 4", len(entries))
	}
}

func TestStoreLookupAndFlags(t *testing.T) {
	b := &fileBackend{dir: t.TempDir()}
	if err := b.Set("x", "secret-x"); err != nil {
		t.Fatal(err)
	}
	s := Store{backend: b, id: "keychain", def: true}
	if !s.DefaultSecretStore() || s.SecretStoreID() != "keychain" {
		t.Fatalf("flags = (%v, %q)", s.DefaultSecretStore(), s.SecretStoreID())
	}
	if !s.Available() {
		t.Fatal("file backend should be available")
	}
	got, found, err := s.Lookup(context.Background(), "x")
	if err != nil || !found || got != "secret-x" {
		t.Fatalf("Lookup = (%q, %v, %v)", got, found, err)
	}
}

func TestStoreUnavailable(t *testing.T) {
	var s Store
	if s.Available() {
		t.Fatal("zero store should be unavailable")
	}
	if _, _, err := s.Lookup(context.Background(), "x"); err == nil {
		t.Fatal("Lookup on zero store should error")
	}
}

func TestFactoryRegistersKeychainImpl(t *testing.T) {
	reg := resource.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	f, ok := reg.Lookup(secret.ResourceKind, ResourceImpl)
	if !ok {
		t.Fatalf("registry missing %s/%s", secret.ResourceKind, ResourceImpl)
	}
	if f.Spec().Kind != secret.ResourceKind || f.Spec().Impl != ResourceImpl {
		t.Fatalf("spec = %+v", f.Spec())
	}
}

func TestFactoryNewDecodesSettings(t *testing.T) {
	dir := t.TempDir()
	f := factory{}
	raw, err := json.Marshal(map[string]any{
		"id": "keychain", "default": true, "dir": dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := f.New(context.Background(), resource.Input{
		Settings: raw,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	store, ok := value.(Store)
	if !ok {
		t.Fatalf("New returned %T, want secrets.Store", value)
	}
	if !store.DefaultSecretStore() || store.SecretStoreID() != "keychain" {
		t.Fatalf("decoded flags = (%v, %q)", store.DefaultSecretStore(), store.SecretStoreID())
	}
	if !store.Available() {
		t.Fatal("store should be available")
	}
}

func TestAccountFor(t *testing.T) {
	if got := AccountFor("deepseek-inst-abc"); got != "inference/deepseek-inst-abc" {
		t.Fatalf("AccountFor = %q", got)
	}
}

func TestManagerSetGetDelete(t *testing.T) {
	m := newFileManager(t.TempDir())
	if !m.Available() {
		t.Fatal("manager should be available")
	}
	if err := m.Set("inference/deepseek-inst-a", "sk-x"); err != nil {
		t.Fatal(err)
	}
	got, found, err := m.Get("inference/deepseek-inst-a")
	if err != nil || !found || got != "sk-x" {
		t.Fatalf("Get = (%q, %v, %v)", got, found, err)
	}
	if err := m.Delete("inference/deepseek-inst-a"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := m.Get("inference/deepseek-inst-a"); found {
		t.Fatal("secret still present after Delete")
	}
}

// newFileManager forces the file backend so tests stay hermetic on
// every platform (NewManager picks the macOS Keychain on darwin).
func newFileManager(dir string) *Manager {
	return &Manager{store: Store{backend: &fileBackend{dir: dir}}}
}
