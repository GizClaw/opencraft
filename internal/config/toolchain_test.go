package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/toolchain"
)

func TestToolchainPreferenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadToolchainPreference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(toolchain.PreferenceExternalFirst) {
		t.Fatalf("missing layer preference = %q, want external-first", got)
	}

	if err := WriteToolchainPreference(
		dir, string(toolchain.PreferenceBundledFirst)); err != nil {
		t.Fatal(err)
	}
	got, err = LoadToolchainPreference(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(toolchain.PreferenceBundledFirst) {
		t.Fatalf("after write = %q, want bundled-first", got)
	}

	if err := WriteToolchainPreference(dir, "sometimes"); err == nil {
		t.Fatal("invalid preference must be rejected")
	}
}

func TestWriteToolchainPreservesOtherResources(t *testing.T) {
	dir := t.TempDir()
	if err := WriteMCP(dir, []MCPServer{
		{Name: "x", Transport: "stdio", Command: "npx"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := WriteToolchainPreference(
		dir, string(toolchain.PreferenceOff)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tool.mcp") ||
		!strings.Contains(string(data), `preference: "off"`) {
		t.Fatalf("cross-resource preservation failed:\n%s", data)
	}
	servers, err := LoadMCP(dir)
	if err != nil || len(servers) != 1 {
		t.Fatalf("MCP after toolchain write = %+v, %v", servers, err)
	}
}
