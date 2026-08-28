package desktop

import (
	"context"
	"testing"
)

func TestSecretExistsAndDelete(t *testing.T) {
	a := fileManagerApp(t, t.TempDir())
	account := secretAccount("auth", "sso-haivivi/token")
	if err := a.secrets.Set(context.Background(), account, "aig_x"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	ok, err := a.SecretExists("auth", "sso-haivivi/token")
	if err != nil || !ok {
		t.Fatalf("SecretExists = (%v, %v), want true", ok, err)
	}
	ok, err = a.SecretExists("auth", "missing")
	if err != nil || ok {
		t.Fatalf("SecretExists(missing) = (%v, %v), want false", ok, err)
	}
	if err := a.SecretDelete("auth", "sso-haivivi/token"); err != nil {
		t.Fatalf("SecretDelete: %v", err)
	}
	ok, _ = a.SecretExists("auth", "sso-haivivi/token")
	if ok {
		t.Fatal("secret still exists after delete")
	}
}

func TestSecretBindingsValidateScopeAndName(t *testing.T) {
	a := fileManagerApp(t, t.TempDir())
	if _, err := a.SecretExists("bogus", "x"); err == nil {
		t.Fatal("unknown scope should fail")
	}
	if _, err := a.SecretExists("auth", "../evil"); err == nil {
		t.Fatal("traversal-style name should fail")
	}
	if _, err := a.SecretExists("auth", ".hidden"); err == nil {
		t.Fatal("dot-leading name should fail")
	}
	if err := a.SecretDelete("auth", ""); err == nil {
		t.Fatal("empty name should fail")
	}
}

func TestSecretBindingsUnavailableStore(t *testing.T) {
	a := &App{}
	if _, err := a.SecretExists("auth", "x"); err == nil {
		t.Fatal("nil store should fail")
	}
	if err := a.SecretDelete("auth", "x"); err == nil {
		t.Fatal("nil store should fail")
	}
}
