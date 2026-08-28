package desktop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/plugins"
)

func TestGatewayProfileUpsertAndRemove(t *testing.T) {
	a := fileManagerApp(t, t.TempDir())
	meta := map[string]any{
		"base_url":      "https://ai.haivivi.cn/v1",
		"default_model": "deepseek-flash",
		"models":        []string{"deepseek-flash", "deepseek-vision"},
		"client_name":   "Haivivi Work",
		"user":          map[string]any{"name": "Richard"},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	account := plugins.SecretAccount("auth", "sso-haivivi/meta")
	if err := a.secrets.Set(context.Background(), account, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := a.secrets.Set(
		context.Background(), plugins.TokenAccount("sso-haivivi"), "aig_x"); err != nil {
		t.Fatal(err)
	}

	if err := a.upsertGatewayProfile("sso-haivivi", ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	cfg, err := config.LoadInference(a.userDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("instances = %+v", cfg.Instances)
	}
	in := cfg.Instances[0]
	if in.StableID != "sso-haivivi" || in.Type != "openai" || in.API != "responses" ||
		in.Endpoint != "https://ai.haivivi.cn/v1" ||
		in.KeySource != config.KeyKeychain ||
		in.KeyValue != plugins.TokenAccount("sso-haivivi") ||
		len(in.Models) != 2 || !in.Enabled {
		t.Fatalf("gateway instance = %+v", in)
	}
	if err := a.removeGatewayProfile("sso-haivivi"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	cfg, _ = config.LoadInference(a.userDir)
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after remove = %+v", cfg.Instances)
	}
}

func TestSecretBindingsDelegate(t *testing.T) {
	a := fileManagerApp(t, t.TempDir())
	if err := a.secrets.Set(context.Background(), plugins.SecretAccount("auth", "x"), "v"); err != nil {
		t.Fatal(err)
	}
	ok, err := a.SecretExists("auth", "x")
	if err != nil || !ok {
		t.Fatalf("SecretExists = (%v, %v)", ok, err)
	}
	if err := a.SecretDelete("auth", "x"); err != nil {
		t.Fatalf("SecretDelete: %v", err)
	}
	ok, _ = a.SecretExists("auth", "x")
	if ok {
		t.Fatal("secret still exists after delete")
	}
	if _, err := a.SecretExists("bogus", "x"); err == nil {
		t.Fatal("unknown scope should fail")
	}
	if _, err := (&App{}).SecretExists("auth", "x"); err == nil {
		t.Fatal("nil store should fail")
	}
}
