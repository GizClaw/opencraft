package bindings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

func writeSimplePlugin(t *testing.T, dataDir, id string) {
	t.Helper()
	dir := filepath.Join(dataDir, "plugins", id)
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"id": "` + id + `",
		"name": "` + id + `",
		"version": "0.1.0",
		"entry": "dist/index.js",
		"permissions": []
	}`
	if err := os.WriteFile(
		filepath.Join(dir, "plugin.json"), []byte(manifest), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func seedPluginInference(t *testing.T, dir, pluginID string) {
	t.Helper()
	cfg := config.InferenceConfig{Instances: []config.Instance{
		{StableID: pluginID + "-main", Type: "deepseek", Name: "Main",
			KeySource: config.KeyLiteral, KeyValue: "key-1", Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash"}}},
		{StableID: pluginID + "-gateway", Type: "deepseek",
			Name: "Gateway", KeySource: config.KeyLiteral,
			KeyValue: "key-2", Enabled: true,
			Models: []config.Model{{Name: "deepseek-v4-flash"}}},
	}}
	owners := map[string]string{
		pluginID + "-main":    pluginID,
		pluginID + "-gateway": pluginID,
	}
	if err := config.WriteInferenceOwned(dir, cfg, owners); err != nil {
		t.Fatal(err)
	}
}

func TestPluginSkillsReturnsSkillDTOs(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins", "plug")
	for _, sub := range []string{"dist", "skills", "skills/hello"} {
		if err := os.MkdirAll(filepath.Join(pluginDir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"plugin.json": `{
			"id": "plug", "name": "Plug", "version": "0.1.0",
			"entry": "dist/index.js",
			"permissions": ["skills:contribute"],
			"skills": ["skills"]
		}`,
		"dist/index.js": "export const apply = () => {};",
		"skills/hello/SKILL.md": "---\nname: hello\n" +
			"description: Plugin skill\n---\nBody.\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(
			filepath.Join(pluginDir, rel), []byte(content), 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}

	b := NewPluginBinding(core.NewCore(dir, dir, ""))
	got, err := b.Skills("plug")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("skills = %+v, want one DTO", got)
	}
	sk := got[0]
	if sk.Name != "hello" || sk.Path == "" ||
		sk.PluginID != "plug" || sk.PluginName != "Plug" {
		t.Fatalf("skill DTO = %+v", sk)
	}
}

func TestPluginDisableRemovesProviderInstances(t *testing.T) {
	dir := t.TempDir()
	writeSimplePlugin(t, dir, "plug")
	seedPluginInference(t, dir, "plug")
	b := NewPluginBinding(core.NewCore(dir, dir, ""))

	if err := b.SetEnabled("plug", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after disable = %+v", cfg.Instances)
	}
	owners, err := config.LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners after disable = %+v", owners)
	}
}

func TestPluginUninstallRemovesInferenceAndSecrets(t *testing.T) {
	dir := t.TempDir()
	writeSimplePlugin(t, dir, "plug")
	seedPluginInference(t, dir, "plug")
	c := core.NewCore(dir, dir, "")
	account := "auth/plug/token"
	if err := c.Plugin.Secrets.Set(
		c.Shell.Context(), account, "secret-value",
	); err != nil {
		t.Fatal(err)
	}
	b := NewPluginBinding(c)

	if err := b.Uninstall("plug"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	cfg, err := config.LoadInference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Fatalf("instances after uninstall = %+v", cfg.Instances)
	}
	owners, err := config.LoadProviderOwners(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners after uninstall = %+v", owners)
	}
	if _, found, err := c.Plugin.Secrets.Get(
		c.Shell.Context(), account,
	); err != nil || found {
		t.Fatalf("secret after uninstall: found=%v err=%v", found, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "plug")); !os.IsNotExist(err) {
		t.Fatalf("plugin dir must be removed, stat err = %v", err)
	}
}
