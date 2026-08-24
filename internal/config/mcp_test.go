package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMCPRoundTripAndCrossPreservation(t *testing.T) {
	dir := t.TempDir()

	// Save inference wiring first, then MCP servers: each write must
	// preserve the other's resources.
	cfg := envKeyed(t, "deepseek")
	if err := WriteInference(dir, cfg); err != nil {
		t.Fatal(err)
	}
	servers := []MCPServer{{
		Name:      "my-server",
		Transport: "stdio",
		Command:   "my-mcp-server",
		Args:      []string{"--x", "1"},
		Env:       map[string]string{"KEY": "value"},
	}, {
		Name:      "remote",
		Transport: "http",
		URL:       "https://example.com/mcp",
	}}
	if err := WriteMCP(dir, servers); err != nil {
		t.Fatal(err)
	}

	got, err := LoadMCP(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("LoadMCP = %+v, want 2 servers", got)
	}
	if got[0].Name != "my-server" || got[0].Command != "my-mcp-server" ||
		len(got[0].Args) != 2 || got[0].Env["KEY"] != "value" {
		t.Fatalf("stdio server = %+v", got[0])
	}
	if got[1].Name != "remote" || got[1].Transport != "http" ||
		got[1].URL != "https://example.com/mcp" {
		t.Fatalf("http server = %+v", got[1])
	}

	// Re-saving inference keeps the MCP resources.
	cfg2 := envKeyed(t, "openai")
	if err := WriteInference(dir, cfg2); err != nil {
		t.Fatal(err)
	}
	got, err = LoadMCP(dir)
	if err != nil || len(got) != 2 {
		t.Fatalf("MCP after WriteInference = %+v, %v", got, err)
	}
	if needed, err := InferenceNeeded(dir); err != nil || needed {
		t.Fatalf("needed after writes = %v, %v", needed, err)
	}

	// Merged view exposes the MCP source.
	view := load(t, t.TempDir(), dir)
	if _, ok := view.Document.Resources["tool.mcp"]; !ok {
		t.Fatal("tool.mcp missing from merged view")
	}

	// Clearing the list keeps a consistent (empty) source.
	if err := WriteMCP(dir, nil); err != nil {
		t.Fatal(err)
	}
	got, err = LoadMCP(dir)
	if err != nil || len(got) != 0 {
		t.Fatalf("LoadMCP after clear = %+v, %v; want empty", got, err)
	}
	view = load(t, t.TempDir(), dir)
	if _, ok := view.Document.Resources["tool.mcp"]; !ok {
		t.Fatal("tool.mcp missing after clear")
	}
}

func TestLoadMCPMissingLayer(t *testing.T) {
	servers, err := LoadMCP(filepath.Join(t.TempDir(), "nope"))
	if err != nil || servers != nil {
		t.Fatalf("LoadMCP(missing) = %+v, %v; want nil, nil", servers, err)
	}
}

func TestWriteMCPRejectsNonMappingUserLayer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "opencraft.yaml"),
		[]byte("just a scalar\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := WriteMCP(dir, []MCPServer{{Name: "x", Transport: "stdio"}}); err == nil {
		t.Fatal("WriteMCP over a non-mapping user layer must fail")
	}
}
