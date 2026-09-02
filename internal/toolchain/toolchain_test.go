package toolchain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveExternalFirst(t *testing.T) {
	root, external := newFixture(t, map[string]string{
		"python": "3.13",
		"go":     "1.25",
		"node":   "24",
		"uv":     "0.12",
	})
	writeStub(t, filepath.Join(external, "python3"), "python3")
	writeStub(t, filepath.Join(root, "python", "3.13", platformKey(), "bin", "python"), "python")
	writeStub(t, filepath.Join(root, "go", "1.25", platformKey(), "bin", "go"), "go")
	writeStub(t, filepath.Join(root, "uv", "0.12", platformKey(), "uvx"), "uvx")
	setExternalPath(t, external)

	m, err := New(Options{
		Preference:   PreferenceExternalFirst,
		Root:         root,
		ManifestPath: filepath.Join(root, "..", "manifest.json"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := m.Resolve(context.Background(), "python3")
	if err != nil {
		t.Fatalf("Resolve(python3): %v", err)
	}
	if got.Source != SourceSystem || got.Path != filepath.Join(external, "python3") {
		t.Fatalf("python3 = %+v, want system stub", got)
	}

	got, err = m.Resolve(context.Background(), "go")
	if err != nil {
		t.Fatalf("Resolve(go): %v", err)
	}
	if got.Source != SourceBundled || got.Family != "go" {
		t.Fatalf("go = %+v, want bundled go", got)
	}
	if got.Version != "1.25.14" {
		t.Fatalf("go version = %q, want manifest 1.25.14", got.Version)
	}

	got, err = m.Resolve(context.Background(), "uvx")
	if err != nil {
		t.Fatalf("Resolve(uvx): %v", err)
	}
	if got.Source != SourceBundled || got.Family != "uv" {
		t.Fatalf("uvx = %+v, want bundled uv", got)
	}
}

func TestResolveOffNeverUsesBundle(t *testing.T) {
	root, external := newFixture(t, map[string]string{"uv": "0.12"})
	writeStub(t, filepath.Join(root, "uv", "0.12", platformKey(), "uv"), "uv")
	setExternalPath(t, external)
	m, err := New(Options{
		Preference:   PreferenceOff,
		Root:         root,
		ManifestPath: filepath.Join(root, "..", "manifest.json"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := m.Resolve(context.Background(), "uv"); err == nil {
		t.Fatal("Resolve(uv) with preference off must not fall back to bundled uv")
	}
}

func TestResolveBundledFirst(t *testing.T) {
	root, external := newFixture(t, map[string]string{"node": "24"})
	writeStub(t, filepath.Join(external, "npx"), "npx")
	writeStub(t, filepath.Join(root, "node", "24", platformKey(), "bin", "npx"), "npx")
	setExternalPath(t, external)
	m, err := New(Options{
		Preference:   PreferenceBundledFirst,
		Root:         root,
		ManifestPath: filepath.Join(root, "..", "manifest.json"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := m.Resolve(context.Background(), "npx")
	if err != nil {
		t.Fatalf("Resolve(npx): %v", err)
	}
	if got.Source != SourceBundled {
		t.Fatalf("npx = %+v, want bundled", got)
	}
}

func TestExternalFirstSkipsOwnLauncherDir(t *testing.T) {
	root, external := newFixture(t, nil)
	launcher := filepath.Join(root, "launcher")
	if err := os.MkdirAll(launcher, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStub(t, filepath.Join(external, "python3"), "python3")
	t.Setenv(
		"PATH",
		launcher+string(os.PathListSeparator)+external+
			string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin",
	)
	m, err := New(Options{
		Preference: PreferenceExternalFirst,
		Root:       root,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := m.Resolve(context.Background(), "python3")
	if err != nil {
		t.Fatalf("Resolve(python3): %v", err)
	}
	if got.Source != SourceSystem ||
		got.Path != filepath.Join(external, "python3") {
		t.Fatalf("python3 = %+v, want external after launcher dir", got)
	}
}

func TestResolveMCPCommand(t *testing.T) {
	m, err := New(Options{Preference: PreferenceExternalFirst})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, command := range []string{
		"/usr/bin/npx", "./bin/server", `C:\bin\npx.exe`,
	} {
		got, err := m.ResolveMCPCommand(command)
		if err != nil || got != command {
			t.Fatalf("ResolveMCPCommand(%q) = (%q, %v)", command, got, err)
		}
	}
	if _, err := m.ResolveMCPCommand("not-a-real-tool-xyz"); err == nil {
		t.Fatal("unknown bare command must fail")
	}
}

func TestSandboxEnv(t *testing.T) {
	root, _ := newFixture(t, nil)
	if err := os.MkdirAll(filepath.Join(root, "launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("OPEN_CRAFT_CACHE", filepath.Join(t.TempDir(), "cache"))
	m, err := New(Options{
		Preference: PreferenceExternalFirst,
		Root:       root,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env := m.SandboxEnv()
	if want := filepath.Join(root, "launcher") + string(os.PathListSeparator) + "/usr/bin:/bin"; env["PATH"] != want {
		t.Fatalf("PATH = %q, want %q", env["PATH"], want)
	}
	for key, want := range map[string]string{
		"GOCACHE":          filepath.Join(os.Getenv("OPEN_CRAFT_CACHE"), "go"),
		"GOMODCACHE":       filepath.Join(os.Getenv("OPEN_CRAFT_CACHE"), "go", "pkg", "mod"),
		"PYTHONUSERBASE":   filepath.Join(os.Getenv("OPEN_CRAFT_CACHE"), "python"),
		"UV_CACHE_DIR":     filepath.Join(os.Getenv("OPEN_CRAFT_CACHE"), "uv"),
		"npm_config_cache": filepath.Join(os.Getenv("OPEN_CRAFT_CACHE"), "npm"),
	} {
		if env[key] != want {
			t.Fatalf("SandboxEnv[%s] = %q, want %q", key, env[key], want)
		}
	}
}

func TestHostEnvSeparateFromSandbox(t *testing.T) {
	sandbox := filepath.Join(t.TempDir(), "sandbox-cache")
	host := filepath.Join(t.TempDir(), "mcp-cache")
	m, err := New(Options{
		Preference:      PreferenceExternalFirst,
		SandboxCacheDir: sandbox,
		HostCacheDir:    host,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	hostEnv := m.HostEnv()
	if got := hostEnv["UV_CACHE_DIR"]; got != filepath.Join(host, "uv") {
		t.Fatalf("host UV_CACHE_DIR = %q, want %q", got, filepath.Join(host, "uv"))
	}
	if got := m.SandboxEnv()["UV_CACHE_DIR"]; got != filepath.Join(sandbox, "uv") {
		t.Fatalf("sandbox UV_CACHE_DIR = %q, want %q", got, filepath.Join(sandbox, "uv"))
	}
}

func TestDiagnoseReportsTools(t *testing.T) {
	root, external := newFixture(t, map[string]string{"go": "1.25"})
	writeStub(t, filepath.Join(external, "node"), "node")
	writeStub(t, filepath.Join(root, "go", "1.25", platformKey(), "bin", "go"), "go")
	setExternalPath(t, external)
	m, err := New(Options{
		Preference:   PreferenceExternalFirst,
		Root:         root,
		ManifestPath: filepath.Join(root, "..", "manifest.json"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	statuses := m.Diagnose(context.Background())
	if len(statuses) != 7 {
		t.Fatalf("Diagnose returned %d entries, want 7", len(statuses))
	}
	byTool := map[string]RuntimeStatus{}
	for _, st := range statuses {
		byTool[st.Tool] = st
	}
	if st := byTool["go"]; st.Source != string(SourceBundled) || st.Error != "" {
		t.Fatalf("go status = %+v", st)
	}
	if st := byTool["node"]; st.Source != string(SourceSystem) || st.Error != "" {
		t.Fatalf("node status = %+v", st)
	}
}

// newFixture writes a valid manifest plus a runtime root. versions is
// the subset of families to stage (minor version directory layout).
func newFixture(t *testing.T, versions map[string]string) (root, external string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "runtime")
	external = filepath.Join(t.TempDir(), "external")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	full := map[string]string{
		"python": "3.13.15",
		"go":     "1.25.14",
		"node":   "24.20.0",
		"uv":     "0.12.9",
	}
	manifest := map[string]any{"schema_version": 1}
	for family, version := range full {
		if _, ok := versions[family]; !ok {
			continue
		}
		platform := platformKey()
		manifest[family] = map[string]any{
			"version": version,
			"urls": map[string]string{
				platform: "https://example.invalid/" + family + ".tar.gz",
			},
			"sha256": map[string]string{
				platform: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "..", "manifest.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return root, external
}

func writeStub(t *testing.T, path, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var data []byte
	if runtime.GOOS == "windows" {
		data = []byte("stub")
	} else {
		data = []byte("#!/bin/sh\nexit 0\n")
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = name
}

func setExternalPath(t *testing.T, external string) {
	t.Helper()
	t.Setenv("PATH", external+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
}
