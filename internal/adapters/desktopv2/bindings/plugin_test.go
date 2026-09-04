package bindings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
)

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
