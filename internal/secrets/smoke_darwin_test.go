//go:build darwin

package secrets

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestKeychainBackendSmoke exercises the real macOS Keychain path
// (Set/Get/Delete via security(1)) to catch interactive-prompt hangs.
// It writes one throwaway item and removes it; skipped unless
// OPEN_CRAFT_KEYCHAIN_SMOKE is set.
func TestKeychainBackendSmoke(t *testing.T) {
	if os.Getenv("OPEN_CRAFT_KEYCHAIN_SMOKE") == "" {
		t.Skip("set OPEN_CRAFT_KEYCHAIN_SMOKE to run against the real Keychain")
	}
	k := &keychainBackend{service: "opencraft"}
	acct := "inference/openai-inst-" + time.Now().Format("150405")
	defer func() { _ = k.Delete(context.Background(), acct) }()
	if err := k.Set(context.Background(), acct, "sk-smoke"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, found, err := k.Get(context.Background(), acct)
	if err != nil || !found || got != "sk-smoke" {
		t.Fatalf("Get = (%q, %v, %v)", got, found, err)
	}
	if err := k.Delete(context.Background(), acct); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
