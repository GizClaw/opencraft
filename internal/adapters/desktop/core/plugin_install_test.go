package core

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/configseed"
)

// writePluginSource writes a minimal plugin tree the registry accepts.
func writePluginSource(t *testing.T, dir, id, version string) {
	t.Helper()
	manifest := fmt.Sprintf(
		`{"id": %q, "name": %q, "version": %q, `+
			`"entry": "dist/index.js", `+
			`"permissions": ["skills:contribute"], `+
			`"skills": ["skills"]}`,
		id, id+" plugin", version)
	files := map[string]string{
		"plugin.json":   manifest,
		"dist/index.js": "export const apply = () => {};",
		"skills/hello/SKILL.md": "---\nname: hello\n" +
			"description: Plugin skill\n---\nBody.\n",
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// writePluginZip packs the same minimal plugin as a .zip package with
// plugin.json at the archive root.
func writePluginZip(t *testing.T, path, id, version string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	zw := zip.NewWriter(file)
	entries := map[string]string{
		"plugin.json": fmt.Sprintf(
			`{"id": %q, "name": %q, "version": %q, `+
				`"entry": "dist/index.js", `+
				`"permissions": ["skills:contribute"], `+
				`"skills": ["skills"]}`,
			id, id+" plugin", version),
		"dist/index.js":         "export const apply = () => {};",
		"skills/hello/SKILL.md": "---\nname: hello\ndescription: Plugin skill\n---\nBody.\n",
	}
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// newInstallerTestCore builds a desktop core with a local sandbox and
// one configured provider, mirroring the plugin skill test harness.
func newInstallerTestCore(t *testing.T) (*Core, string, string) {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "test-key")
	workDir := t.TempDir()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := []byte(
		"version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(
		filepath.Join(configDir, "opencraft.yaml"), seed, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      "openai",
			KeySource: config.KeyEnv,
			Enabled:   true,
			Models:    []config.Model{{Name: "test-model"}},
		}},
	}
	if err := configseed.Write(configDir, cfg); err != nil {
		t.Fatal(err)
	}
	c := NewCore(configDir, dataDir, "")
	c.SetWorkDir(workDir)
	return c, workDir, dataDir
}

// TestPluginInstallerInstallsAndReloads pins the host-side install the
// agent's plugin_install tool drives: the source is copied into the
// registry, and the active workspace runtime is reassembled so the
// plugin's contributions are live.
func TestPluginInstallerInstallsAndReloads(t *testing.T) {
	c, workDir, dataDir := newInstallerTestCore(t)
	ctx := context.Background()
	// Warm the runtime so the install has a pooled Host to replace.
	if _, err := c.Runtime.Acquire(ctx, workDir, interact.Auto{}); err != nil {
		t.Fatalf("acquire host: %v", err)
	}

	src := filepath.Join(workDir, ".opencraft-plugins", "hello")
	writePluginSource(t, src, "hello", "0.1.0")

	inst := NewPluginInstaller(c)
	if empty, ok := inst.(interface{ Empty() bool }); !ok || empty.Empty() {
		t.Fatal("desktop installer must be non-empty")
	}
	sum, err := inst.PluginInstall(ctx, src)
	if err != nil {
		t.Fatalf("PluginInstall: %v", err)
	}
	if sum.ID != "hello" || sum.Version != "0.1.0" || !sum.Enabled {
		t.Fatalf("summary = %+v", sum)
	}
	if _, err := os.Stat(filepath.Join(
		dataDir, "plugins", "hello", "plugin.json",
	)); err != nil {
		t.Fatalf("registry copy missing: %v", err)
	}

	// The reload replaced the pooled Host, so the new plugin's skill is
	// part of the freshly assembled registry.
	h := c.Runtime.Current()
	if h == nil {
		t.Fatal("no current host after install")
	}
	value, ok := h.Controller().Runtime().Resource("skills")
	if !ok {
		t.Fatal("skills resource missing after reload")
	}
	svc, ok := value.(*skills.Service)
	if !ok || svc == nil {
		t.Fatal("skills resource is not *skills.Service")
	}
	found := false
	for _, sk := range svc.List() {
		if sk.Name == "hello" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("plugin skill not discovered after install: roots=%v errors=%v",
			svc.Roots(), svc.Errors())
	}

	// plugin_list reports the registry.
	list, err := inst.PluginsList(ctx)
	if err != nil {
		t.Fatalf("PluginsList: %v", err)
	}
	if len(list) != 1 || list[0].ID != "hello" {
		t.Fatalf("PluginsList = %+v", list)
	}

	// Iteration: a newer version replaces the installed one and leaves
	// a rollback snapshot behind.
	next := filepath.Join(workDir, ".opencraft-plugins", "hello-next")
	writePluginSource(t, next, "hello", "0.2.0")
	sum, err = inst.PluginUpdate(ctx, "hello", next)
	if err != nil {
		t.Fatalf("PluginUpdate: %v", err)
	}
	if sum.Version != "0.2.0" {
		t.Fatalf("updated summary = %+v", sum)
	}
	if _, err := os.Stat(filepath.Join(
		dataDir, "plugins", ".backups", "hello", "plugin.json",
	)); err != nil {
		t.Fatalf("rollback snapshot missing: %v", err)
	}
}

// TestPluginInstallerInspectRejectsInvalidSource pins that a source
// without a valid manifest never reaches the registry.
func TestPluginInstallerInspectRejectsInvalidSource(t *testing.T) {
	c, workDir, dataDir := newInstallerTestCore(t)
	ctx := context.Background()
	src := filepath.Join(workDir, "broken")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	inst := NewPluginInstaller(c)
	if _, err := inst.PluginInspect(ctx, src); err == nil {
		t.Fatal("source without plugin.json must fail inspection")
	}
	if _, err := inst.PluginInstall(ctx, src); err == nil {
		t.Fatal("invalid source must not install")
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "plugins"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("registry changed by a failed install: %v", entries)
	}
}

// TestPluginInstallerInstallsZipPackage pins the package path: the
// agent may hand over a .zip the user prefers to one folder.
func TestPluginInstallerInstallsZipPackage(t *testing.T) {
	c, workDir, dataDir := newInstallerTestCore(t)
	ctx := context.Background()
	pkg := filepath.Join(workDir, "hello-0.1.0.zip")
	writePluginZip(t, pkg, "hello", "0.1.0")

	inst := NewPluginInstaller(c)
	if _, err := inst.PluginInspect(ctx, pkg); err != nil {
		t.Fatalf("PluginInspect(zip): %v", err)
	}
	sum, err := inst.PluginInstall(ctx, pkg)
	if err != nil {
		t.Fatalf("PluginInstall(zip): %v", err)
	}
	if sum.ID != "hello" || sum.Version != "0.1.0" {
		t.Fatalf("summary = %+v", sum)
	}
	if _, err := os.Stat(filepath.Join(
		dataDir, "plugins", "hello", "skills", "hello", "SKILL.md",
	)); err != nil {
		t.Fatalf("zip contents missing from the registry: %v", err)
	}
}

// TestNilCoreInstallerIsEmpty pins the defensive shape used by
// runtimes whose plugin service was never assembled.
func TestNilCoreInstallerIsEmpty(t *testing.T) {
	inst := NewPluginInstaller(nil)
	if empty, ok := inst.(interface{ Empty() bool }); !ok || !empty.Empty() {
		t.Fatal("installer over a nil core must be empty")
	}
}
