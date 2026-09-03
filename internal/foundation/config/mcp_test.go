package config

import (
	"os"
	"path/filepath"
	"strings"
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

	// Clearing the list removes the source and its tools dep so the
	// merged document stays valid (an MCP source without servers is
	// rejected by the runtime).
	if err := WriteMCP(dir, nil); err != nil {
		t.Fatal(err)
	}
	got, err = LoadMCP(dir)
	if err != nil || len(got) != 0 {
		t.Fatalf("LoadMCP after clear = %+v, %v; want empty", got, err)
	}
	view = load(t, t.TempDir(), dir)
	if _, ok := view.Document.Resources["tool.mcp"]; ok {
		t.Fatal("tool.mcp must be absent after clear")
	}
}

// TestWriteMCPPreservesProviderResources guards the regression where
// saving MCP servers dropped every provider.* resource from a
// hand-written user layer (provider keys belong to the inference
// writer only).
func TestWriteMCPPreservesProviderResources(t *testing.T) {
	dir := t.TempDir()
	existing := `version: v1
resources:
  provider.azure:
    kind: inference.Provider
    impl: azure
    settings:
      id: azure
      spec:
        endpoint: https://example.openai.azure.com
        models:
          - name: gpt-5.6-sol
            kind: generate
            capabilities:
              outputs: [text]
      profiles:
        - secrets:
            api_key: ${env:AZURE_OPENAI_API_KEY}
  infer:
    deps:
      provider.azure: provider.azure
  router:
    settings:
      generate:
        - tier: default
          targets:
            - model:
                id:
                  provider: azure
                  name: gpt-5.6-sol
  box:
    settings:
      remote: false
  execpolicy:
    settings:
      allowed_commands: ["git status"]
agents:
  reviewer:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(dir, "opencraft.yaml"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteMCP(dir, []MCPServer{{
		Name:      "my-server",
		Transport: "stdio",
		Command:   "my-mcp-server",
	}}); err != nil {
		t.Fatal(err)
	}
	merged, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"provider.azure",
		"infer",
		"router",
		"tool.mcp",
		"box",
		"execpolicy",
		"agents",
		"reviewer",
	} {
		if !strings.Contains(string(merged), want) {
			t.Fatalf("merged user layer missing %q:\n%s", want, merged)
		}
	}
}

func TestWriteMCPClearPreservesOtherToolsDeps(t *testing.T) {
	dir := t.TempDir()
	existing := `version: v1
resources:
  tools:
    deps:
      tool.exec: tool.exec
      tool.mcp: tool.mcp
`
	if err := os.WriteFile(filepath.Join(dir, "opencraft.yaml"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteMCP(dir, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tool.exec") {
		t.Fatalf("clearing MCP dropped unrelated tools dep:\n%s", data)
	}
	if strings.Contains(string(data), "tool.mcp") {
		t.Fatalf("clearing MCP left stale tool.mcp dep:\n%s", data)
	}
}

func TestLoadMCPMissingLayer(t *testing.T) {
	servers, err := LoadMCP(filepath.Join(t.TempDir(), "nope"))
	if err != nil || servers == nil || len(servers) != 0 {
		t.Fatalf("LoadMCP(missing) = %+v, %v; want empty slice, nil", servers, err)
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
