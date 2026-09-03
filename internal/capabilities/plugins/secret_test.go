package plugins

import "testing"

func TestValidateSecretRef(t *testing.T) {
	if err := ValidateSecretRef("auth", "sso-haivivi/token"); err != nil {
		t.Fatalf("valid ref rejected: %v", err)
	}
	if err := ValidateSecretRef("bogus", "x"); err == nil {
		t.Fatal("unknown scope should fail")
	}
	if err := ValidateSecretRef("auth", "../evil"); err == nil {
		t.Fatal("traversal name should fail")
	}
	if err := ValidateSecretRef("auth", ".hidden"); err == nil {
		t.Fatal("dot-leading name should fail")
	}
	if err := ValidateSecretRef("auth", ""); err == nil {
		t.Fatal("empty name should fail")
	}
}

func TestSecretAccounts(t *testing.T) {
	if got := SecretAccount("auth", "haivivi/token"); got != "auth/haivivi/token" {
		t.Fatalf("SecretAccount = %q", got)
	}
}
