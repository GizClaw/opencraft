package config

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

// TestEmbeddedAssetsKeepContentOffTheStateRoot pins the two-root split
// the deploy document relies on: content and credentials (keyring,
// agents, user skills, hooks, the plugin registry) live under the app
// home, which every instance of one user shares, while the state root
// (workspaces, user.db, logs, audit, cache) belongs to one process.
//
// A ${ocraft:DATA_DIR} reference in an embedded asset would silently
// send a dev profile at the installed app's content — the exact failure
// the split exists to prevent — so the contract is "none". The resolver
// still provides the value: external plugin manifests may ask for it.
func TestEmbeddedAssetsKeepContentOffTheStateRoot(t *testing.T) {
	const forbidden = "${ocraft:DATA_DIR}"
	root := FS()
	err := fs.WalkDir(root, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(p) != ".yaml" {
			return nil
		}
		data, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), forbidden) {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, forbidden) {
				t.Errorf("%s:%d references %s: %s",
					p, i+1, forbidden, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded assets: %v", err)
	}
}

// TestEmbeddedAssetsResolveAppHome pins the other half of the same
// contract: the content paths must keep pointing at the app home value,
// so a rename of the resolver value cannot slip through unnoticed.
func TestEmbeddedAssetsResolveAppHome(t *testing.T) {
	root := FS()
	// opencraft.yaml: keychain dir, user skills root, hooks path. The
	// file-tool staging roots moved to ${ocraft:CACHE} instead, which is
	// state (the execd runner cache lives there too).
	want := map[string]int{
		"assets/opencraft.yaml": 3,
		"assets/agents.yaml":    1,
		"assets/tools.yaml":     2,
	}
	for file, count := range want {
		data, err := fs.ReadFile(root, file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		got := strings.Count(string(data), "${ocraft:APP_HOME}")
		if got != count {
			t.Errorf("%s: %d ${ocraft:APP_HOME} references, want %d",
				file, got, count)
		}
	}
}
