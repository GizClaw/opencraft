package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

func TestNewCoreWiresPluginSkillsIntoRuntime(t *testing.T) {
	for _, key := range []string{
		"OPEN_CRAFT_WORKDIR",
		"OPEN_CRAFT_CACHE",
		"OPEN_CRAFT_DATA_DIR",
		"OPEN_CRAFT_WORKSPACE_DIR",
		"OPEN_CRAFT_SESSIONS_DIR",
		"OPEN_CRAFT_APPROVALS",
		"OPEN_CRAFT_TOOL_CACHE",
		"OPEN_CRAFT_AUDIT_DIR",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	workDir := t.TempDir()
	dataDir := t.TempDir()
	configDir := t.TempDir()

	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := []byte("version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(
		filepath.Join(configDir, "opencraft.yaml"), seed, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      "deepseek",
			KeySource: config.KeyEnv,
			Enabled:   true,
		}},
	}
	if err := config.WriteInference(configDir, cfg); err != nil {
		t.Fatal(err)
	}

	pluginDir := filepath.Join(dataDir, "plugins", "plug")
	for _, dir := range []string{
		filepath.Join(pluginDir, "dist"),
		filepath.Join(pluginDir, "skills", "hello"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
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

	c := NewCore(configDir, dataDir, "")
	c.SetWorkDir(workDir)
	ctx := context.Background()
	h, err := c.Runtime.Acquire(ctx, workDir, interact.Auto{})
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	value, ok := h.Controller().Runtime().Resource("skills")
	if !ok {
		t.Fatal("skills resource missing")
	}
	svc, ok := value.(*skills.Service)
	if !ok || svc == nil {
		t.Fatal("skills resource is not *skills.Service")
	}
	for _, sk := range svc.List() {
		if sk.Name == "hello" {
			return
		}
	}
	t.Fatalf("plugin skill not discovered: roots=%v scan=%v errors=%v",
		svc.Roots(), svc.List(), svc.Errors())
}
