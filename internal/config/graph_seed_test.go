package config

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
)

// TestGraphSeededAndFileResolvable verifies the dynamically editable
// graph: the default graph and its referenced files are seeded into
// the user config dir, and every file source in the graph definition
// resolves against that dir through the deploy loader.
func TestGraphSeededAndFileResolvable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfgDir, err := EnsureUserConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"graphs/assistant.yaml",
		"graphs/node/world.js",
		"prompts/system.md",
	} {
		target := filepath.Join(cfgDir, filepath.FromSlash(name))
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("seeded %s: %v", name, err)
		}
	}

	loader := resource.NewLoader(
		resource.WithBaseDir(cfgDir),
		resource.WithEmbed(FS()),
	)
	for _, src := range []resource.Source{
		{File: "graphs/assistant.yaml"},
		{File: "graphs/node/world.js"},
		{File: "prompts/system.md"},
	} {
		data, err := loader.Load(context.Background(), src)
		if err != nil {
			t.Fatalf("load %v: %v", src, err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			t.Fatalf("load %v: empty", src)
		}
	}

	// The embedded base document must reference the graph as a file
	// source so the seeded copy is the one used at runtime.
	base, err := EmbeddedOpenCraft()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(base, []byte("file: graphs/assistant.yaml")) {
		t.Fatal("embedded opencraft.yaml does not reference the graph via file source")
	}
}
