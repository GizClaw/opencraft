package host

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
)

// TestOpenUserDBInstallsMemoryTextGuard pins the wiring: the secret
// rules the deploy document defines for stored text reach the memory
// store when user.db opens, so a write that never goes through the
// remember tool (the settings card, an accepted review suggestion) is
// refused too.
func TestOpenUserDBInstallsMemoryTextGuard(t *testing.T) {
	m := NewManagerAt(t.TempDir(), t.TempDir())
	ctx := context.Background()
	t.Cleanup(func() {
		m.CloseUserDB()
		userstore.SetTextRedactor(nil)
	})
	if err := m.OpenUserDB(ctx); err != nil {
		t.Fatalf("open user db: %v", err)
	}
	secret := userstore.Fact{
		Text:  "the deploy key is sk-abcdefghijklmnopqrstuvwx",
		Scope: userstore.ScopeGlobal,
	}
	if _, err := userstore.Validate(secret); !errdefs.IsValidation(err) {
		t.Fatalf("secret-shaped text err = %v, want a validation error", err)
	}
	clean := userstore.Fact{Text: "prefers metric units", Scope: userstore.ScopeGlobal}
	if _, err := userstore.Validate(clean); err != nil {
		t.Fatalf("plain text refused: %v", err)
	}
}
