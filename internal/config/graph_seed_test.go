package config

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
)

// TestUserConfigAndGraphNotSeeded verifies EnsureUserConfig creates no
// config documents (the first-run wizard owns the user layer) and the
// default graph stays in the binary: neither the graph nor its node
// sources are seeded, and the embedded graph definition resolves its
// node sources through the same embed FS at load time.
func TestUserConfigAndGraphNotSeeded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfgDir, err := EnsureUserConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"opencraft.yaml", "inference.yaml"} {
		target := filepath.Join(cfgDir, filepath.FromSlash(name))
		if _, err := os.Stat(target); err == nil {
			t.Fatalf("config document %s must not be seeded (wizard-owned)", name)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", name, err)
		}
	}
	for _, name := range []string{
		"graphs/assistant.yaml",
		"graphs/nodes/world.js",
		"graphs/nodes/compact.js",
		"graphs/prompts/system.md",
	} {
		if _, err := os.Stat(filepath.Join(cfgDir, filepath.FromSlash(name))); err == nil {
			t.Fatalf("default graph asset %s must not be seeded to the user dir", name)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", name, err)
		}
	}

	loader := resource.NewLoader(
		resource.WithBaseDir(cfgDir),
		resource.WithEmbed(FS()),
	)
	for _, src := range []resource.Source{
		{Embed: "assets/graphs/assistant.yaml"},
		{Embed: "assets/inference.yaml"},
		{Embed: "assets/graphs/nodes/world.js"},
		{Embed: "assets/graphs/nodes/compact.js"},
		{Embed: "assets/graphs/prompts/system.md"},
	} {
		data, err := loader.Load(context.Background(), src)
		if err != nil {
			t.Fatalf("load %v: %v", src, err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			t.Fatalf("load %v: empty", src)
		}
	}

	// The embedded base document must reference the graph as an embed
	// source so the binary copy is the one used at runtime, and the
	// graph's node sources must do the same.
	base, err := EmbeddedOpenCraft()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(base, []byte("embed: assets/graphs/assistant.yaml")) {
		t.Fatal("embedded opencraft.yaml does not reference the graph via embed source")
	}
	graph, err := loader.Load(context.Background(),
		resource.Source{Embed: "assets/graphs/assistant.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{
		"embed: assets/graphs/nodes/world.js",
		"embed: assets/graphs/nodes/compact.js",
		"embed: assets/graphs/prompts/system.md",
	} {
		if !bytes.Contains(graph, []byte(ref)) {
			t.Fatalf("embedded graph does not reference %s via embed source", ref)
		}
	}
}
