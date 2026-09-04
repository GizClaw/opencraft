package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"

	pluginruntime "github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

func TestPluginSessionImportRequiresPermission(t *testing.T) {
	c := NewCore(t.TempDir(), t.TempDir(), "")
	_, err := c.handlePluginSessionImport("unknown",
		pluginruntime.SessionImportRequest{BundlePath: "/tmp/conv.json"})
	if err == nil || !strings.Contains(err.Error(), "lacks sessions:import") {
		t.Fatalf("error = %v, want permission error", err)
	}
}

func TestPluginSessionImportRequiresWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	writeTestPlugin(t, dataDir, "plug", []string{"sessions:import"})
	c := NewCore(t.TempDir(), dataDir, "")
	_, err := c.handlePluginSessionImport("plug",
		pluginruntime.SessionImportRequest{BundlePath: "/tmp/conv.json"})
	if err == nil || !strings.Contains(err.Error(), "no workspace selected") {
		t.Fatalf("error = %v, want no-workspace error", err)
	}
}

func TestPluginSessionImportWiresHostWritePath(t *testing.T) {
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
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "imported"})
	workDir := t.TempDir()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProviderConfig(t, configDir, provider.URL())
	writeTestPlugin(t, dataDir, "plug", []string{"sessions:import"})

	c := NewCore(configDir, dataDir, "")
	c.SetWorkDir(workDir)
	ctx := context.Background()
	h, err := c.Runtime.Acquire(ctx, workDir, interact.Auto{})
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	bundle := ocsessions.ImportRequest{
		Title:  "codex import",
		Source: "codex:test-session",
		Turns: []ocsessions.ImportTurn{{
			Messages: []message.Message{
				message.NewTextMessage(message.RoleUser, "remember me"),
				message.NewTextMessage(message.RoleAssistant, "noted"),
			},
		}},
	}
	bundlePath := filepath.Join(t.TempDir(), "conv.json")
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundlePath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := c.handlePluginSessionImport("plug",
		pluginruntime.SessionImportRequest{BundlePath: bundlePath})
	if err != nil {
		t.Fatalf("handlePluginSessionImport: %v", err)
	}
	if !strings.HasPrefix(res.SessionID, "s-") {
		t.Fatalf("session id = %q, want s- prefix", res.SessionID)
	}
	if res.Turns != 1 || res.Messages != 2 {
		t.Fatalf("result = %+v, want 1 turn / 2 messages", res)
	}
	ready, err := h.Sessions().ImportReady(ctx, res.SessionID)
	if err != nil || !ready {
		t.Fatalf("ImportReady(%q) = %v, %v", res.SessionID, ready, err)
	}
	memoryCount := countTestMemory(t, h, res.SessionID)
	if memoryCount == 0 {
		t.Fatal("imported session has no memory rows")
	}

	again, err := c.handlePluginSessionImport("plug",
		pluginruntime.SessionImportRequest{BundlePath: bundlePath})
	if err != nil {
		t.Fatalf("duplicate import: %v", err)
	}
	if again.SessionID != res.SessionID {
		t.Fatalf("duplicate import = %q, want %q",
			again.SessionID, res.SessionID)
	}
	if got := countTestMemory(t, h, res.SessionID); got != memoryCount {
		t.Fatalf("memory rows after duplicate import = %d, want %d",
			got, memoryCount)
	}
}

func writeTestPlugin(t *testing.T, dataDir, id string, perms []string) {
	t.Helper()
	pluginDir := filepath.Join(dataDir, "plugins", id)
	if err := os.MkdirAll(filepath.Join(pluginDir, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	permsJSON, err := json.Marshal(perms)
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"id": "` + id + `",
		"name": "Test Plugin",
		"version": "0.1.0",
		"entry": "dist/index.js",
		"permissions": ` + string(permsJSON) + `
	}`
	if err := os.WriteFile(
		filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(pluginDir, "dist", "index.js"),
		[]byte("export const apply = () => {};"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func writeProviderConfig(t *testing.T, configDir, baseURL string) {
	t.Helper()
	seed := []byte("version: v1\nresources:\n  box:\n    settings:\n      remote: false\n")
	if err := os.WriteFile(
		filepath.Join(configDir, "opencraft.yaml"), seed, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	cfg := config.InferenceConfig{
		Instances: []config.Instance{{
			Type:      "openai",
			Name:      "fake",
			API:       "chat",
			Endpoint:  baseURL,
			Enabled:   true,
			KeySource: config.KeyLiteral,
			KeyValue:  "test-key",
			Models:    []config.Model{{Name: "fake-model"}},
		}},
	}
	if err := config.WriteInference(configDir, cfg); err != nil {
		t.Fatal(err)
	}
}

func countTestMemory(t *testing.T, h *host.Host, id string) int {
	t.Helper()
	var count int
	if err := h.Sessions().Database().SQLDB().QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM memory_items WHERE thread_id = ?`,
		id,
	).Scan(&count); err != nil {
		t.Fatalf("count imported memory: %v", err)
	}
	return count
}
