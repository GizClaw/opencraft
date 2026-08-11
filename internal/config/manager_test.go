package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func inferenceYAML() string {
	return `version: v1
providers:
  - id: deepseek
    driver: deepseek
    profiles:
      - secrets:
          api_key:
            resolver: env
            key: DEEPSEEK_API_KEY
route:
  generate:
    - tier: primary
      targets:
        - model:
            id:
              provider: deepseek
              name: deepseek-v4-flash
`
}

func TestLoadUserDocuments(t *testing.T) {
	userDir := t.TempDir()
	writeConfig(t, userDir, "inference.yaml", inferenceYAML())
	writeConfig(t, userDir, "workspace.yaml", "version: v1\nworkspaces:\n  main:\n    driver: local\n    settings:\n      root: .\n")
	writeConfig(t, userDir, "tools.yaml", "version: v1\nsources:\n  - kind: builtin\n    spec:\n      tools: [exec_command]\n")
	writeConfig(t, userDir, "sandbox.yaml", "version: v1\nsandboxes:\n  main:\n    backend: seatbelt\n    workspace: main\n    defaults:\n      timeout: 5m\n")

	mgr, err := Open(Options{WorkDir: t.TempDir(), UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Inference == nil || view.Workspace == nil ||
		view.Tools == nil || view.Sandbox == nil {
		t.Fatalf("incomplete view: %+v", view)
	}
	if view.Sandbox.Sandboxes["main"].Defaults.Timeout == 0 {
		t.Error("sandbox timeout not parsed")
	}
	if mgr.ProjectDir() != "" {
		t.Errorf("unexpected project dir %q", mgr.ProjectDir())
	}
}

func TestLoadProjectOverridesSandbox(t *testing.T) {
	userDir := t.TempDir()
	workDir := t.TempDir()
	writeConfig(t, userDir, "inference.yaml", inferenceYAML())
	writeConfig(t, userDir, "workspace.yaml", "version: v1\nworkspaces:\n  main:\n    driver: local\n    settings:\n      root: .\n")
	writeConfig(t, userDir, "tools.yaml", "version: v1\nsources:\n  - kind: builtin\n    spec:\n      tools: [exec_command]\n")
	writeConfig(t, userDir, "sandbox.yaml", "version: v1\nsandboxes:\n  main:\n    backend: seatbelt\n    workspace: main\n    defaults:\n      timeout: 5m\n")
	writeConfig(t, filepath.Join(workDir, ".opencraft", "config"),
		"sandbox.yaml", "version: v1\nsandboxes:\n  main:\n    defaults:\n      timeout: 30m\n")

	mgr, err := Open(Options{WorkDir: workDir, UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(view.Sandbox.Sandboxes["main"].Defaults.Timeout); got != 30*time.Minute {
		t.Errorf("timeout = %v, want 30m", got)
	}
	if view.Origins["sandbox.yaml.sandboxes.main.defaults.timeout"] != "project" {
		t.Errorf("origins = %v", view.Origins)
	}
	if !strings.Contains(string(view.Raw["sandbox.yaml"]), "30m") {
		t.Errorf("raw merged = %s", view.Raw["sandbox.yaml"])
	}
	if mgr.ProjectDir() == "" {
		t.Fatal("project dir not discovered")
	}
}

func TestLoadProjectReplacesToolsArray(t *testing.T) {
	userDir := t.TempDir()
	workDir := t.TempDir()
	writeConfig(t, userDir, "inference.yaml", inferenceYAML())
	writeConfig(t, userDir, "workspace.yaml", "version: v1\nworkspaces:\n  main:\n    driver: local\n    settings:\n      root: .\n")
	writeConfig(t, userDir, "tools.yaml", "version: v1\nsources:\n  - kind: builtin\n    spec:\n      tools: [exec_command]\n")
	writeConfig(t, userDir, "sandbox.yaml", "version: v1\nsandboxes:\n  main:\n    backend: seatbelt\n    workspace: main\n")
	writeConfig(t, filepath.Join(workDir, ".opencraft", "config"),
		"tools.yaml", "version: v1\nsources:\n  - kind: mcp\n    spec:\n      servers:\n        - name: extra\n")

	mgr, err := Open(Options{WorkDir: workDir, UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw := string(view.Raw["tools.yaml"])
	if !strings.Contains(raw, `"name":"extra"`) || strings.Contains(raw, "exec_command") {
		t.Errorf("merged tools = %s (array must be replaced)", raw)
	}
}

func TestValidateFailsOnBadDocument(t *testing.T) {
	userDir := t.TempDir()
	writeConfig(t, userDir, "tools.yaml", "version: v2\nsources: []\n")
	writeConfig(t, userDir, "sandbox.yaml", "version: v1\nsandboxes:\n  main:\n    backend: seatbelt\n    workspace: main\n")

	mgr, err := Open(Options{WorkDir: t.TempDir(), UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Validate(context.Background()); err == nil {
		t.Fatal("Validate unexpectedly succeeded on unknown source kind")
	}
}

func TestUpdateProjectLayerCreatesAndMerges(t *testing.T) {
	userDir := t.TempDir()
	workDir := t.TempDir()
	writeConfig(t, userDir, "inference.yaml", inferenceYAML())
	writeConfig(t, userDir, "workspace.yaml", "version: v1\nworkspaces:\n  main:\n    driver: local\n    settings:\n      root: .\n")
	writeConfig(t, userDir, "tools.yaml", "version: v1\nsources:\n  - kind: builtin\n    spec:\n      tools: [exec_command]\n")
	writeConfig(t, userDir, "sandbox.yaml", "version: v1\nsandboxes:\n  main:\n    backend: seatbelt\n    workspace: main\n    defaults:\n      timeout: 5m\n")

	mgr, err := Open(Options{WorkDir: workDir, UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Update(context.Background(), "sandbox", LayerProject, map[string]any{
		"sandboxes": map[string]any{
			"main": map[string]any{
				"defaults": map[string]any{"timeout": "30m"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if mgr.ProjectDir() == "" {
		t.Fatal("project dir should be created")
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(view.Sandbox.Sandboxes["main"].Defaults.Timeout); got != 30*time.Minute {
		t.Errorf("timeout = %v, want 30m", got)
	}
	// User layer must be untouched.
	userData, err := os.ReadFile(filepath.Join(userDir, "sandbox.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(userData), "30m") {
		t.Errorf("user layer was modified: %s", userData)
	}
}

func TestUpdateUserLayer(t *testing.T) {
	userDir := t.TempDir()
	writeConfig(t, userDir, "inference.yaml", inferenceYAML())
	writeConfig(t, userDir, "workspace.yaml", "version: v1\nworkspaces:\n  main:\n    driver: local\n    settings:\n      root: .\n")
	writeConfig(t, userDir, "tools.yaml", "version: v1\nsources:\n  - kind: builtin\n    spec:\n      tools: [exec_command]\n")
	writeConfig(t, userDir, "sandbox.yaml", "version: v1\nsandboxes:\n  main:\n    backend: seatbelt\n    workspace: main\n")

	mgr, err := Open(Options{WorkDir: t.TempDir(), UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Update(context.Background(), "tools", LayerUser, map[string]any{
		"sources": []any{
			map[string]any{
				"kind": "mcp",
				"spec": map[string]any{
					"servers": []any{map[string]any{"name": "extra"}},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Tools.Sources) != 1 || view.Tools.Sources[0].Kind != "mcp" {
		t.Errorf("sources = %+v", view.Tools.Sources)
	}
}

func TestUpdateRejectsInvalidPatchWithoutWriting(t *testing.T) {
	userDir := t.TempDir()
	writeConfig(t, userDir, "inference.yaml", inferenceYAML())
	writeConfig(t, userDir, "workspace.yaml", "version: v1\nworkspaces:\n  main:\n    driver: local\n    settings:\n      root: .\n")
	writeConfig(t, userDir, "tools.yaml", "version: v1\nsources:\n  - kind: builtin\n    spec:\n      tools: [exec_command]\n")
	writeConfig(t, userDir, "sandbox.yaml", "version: v1\nsandboxes:\n  main:\n    backend: seatbelt\n    workspace: main\n")

	mgr, err := Open(Options{WorkDir: t.TempDir(), UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(userDir, "sandbox.yaml"))
	if err := mgr.Update(context.Background(), "sandbox", LayerUser, map[string]any{
		"version": "v99",
	}); err == nil {
		t.Fatal("Update unexpectedly accepted invalid version")
	}
	after, _ := os.ReadFile(filepath.Join(userDir, "sandbox.yaml"))
	if string(before) != string(after) {
		t.Error("file was modified despite invalid patch")
	}
}
