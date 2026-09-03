package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/secrets"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func fileManagerApp(t *testing.T, dir string) *App {
	t.Helper()
	return &App{
		userDir: dir,
		secrets: secrets.NewFileManager(filepath.Join(dir, "keyring")),
	}
}

func TestSaveInferenceStoresNewKeyInCredentialStore(t *testing.T) {
	dir := t.TempDir()
	a := fileManagerApp(t, dir)
	err := a.saveInference(InferenceRequest{Instances: []ProviderInstance{{
		Type:    "deepseek",
		Name:    "DeepSeek",
		Key:     "sk-new",
		Models:  []ModelView{{Name: "deepseek-v4-flash"}},
		Enabled: true,
	}}})
	if err != nil {
		t.Fatalf("saveInference: %v", err)
	}
	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	in := cfg.Instances[0]
	if in.KeySource != config.KeyKeychain || in.KeyValue == "" {
		t.Fatalf("KeySource = %v, KeyValue = %q, want KeyKeychain + account",
			in.KeySource, in.KeyValue)
	}
	got, found, err := a.secrets.Get(context.Background(), in.KeyValue)
	if err != nil || !found || got != "sk-new" {
		t.Fatalf("store Get = (%q, %v, %v), want sk-new", got, found, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-new") {
		t.Fatal("plaintext key leaked into opencraft.yaml")
	}
}

func TestSaveInferenceInheritsKeychainRef(t *testing.T) {
	dir := t.TempDir()
	a := fileManagerApp(t, dir)
	if err := a.saveInference(InferenceRequest{Instances: []ProviderInstance{{
		Type:    "deepseek",
		Name:    "DeepSeek",
		Key:     "sk-first",
		Models:  []ModelView{{Name: "deepseek-v4-flash"}},
		Enabled: true,
	}}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	stableID := cfg.Instances[0].StableID
	account := cfg.Instances[0].KeyValue

	// Blank key + same stable id must keep the keychain reference.
	if err := a.saveInference(InferenceRequest{Instances: []ProviderInstance{{
		StableID: stableID,
		Type:     "deepseek",
		Name:     "DeepSeek",
		Key:      "",
		Models:   []ModelView{{Name: "deepseek-v4-flash"}},
		Enabled:  true,
	}}}); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Instances[0].KeySource != config.KeyKeychain ||
		cfg.Instances[0].KeyValue != account {
		t.Fatalf("inherited = (%v, %q), want keychain ref %q",
			cfg.Instances[0].KeySource, cfg.Instances[0].KeyValue, account)
	}
	if got, found, _ := a.secrets.Get(context.Background(), account); !found || got != "sk-first" {
		t.Fatalf("store value = (%q, %v), want sk-first", got, found)
	}
}
