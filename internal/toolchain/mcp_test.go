package toolchain

import (
	"context"
	"testing"
)

func TestMCPTransportKeepsUnresolvableBareCommand(t *testing.T) {
	mgr, err := New(Options{Preference: PreferenceExternalFirst})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A configured server command that cannot be resolved today must
	// not fail runtime assembly; the MCP source surfaces it as a
	// connection error instead.
	_, err = mcpTransport(context.Background(), MCPServer{
		Name:      "server",
		Transport: "stdio",
		Command:   "definitely-not-a-real-command-xyz",
	}, mgr)
	if err != nil {
		t.Fatalf("mcpTransport must stay resolvable-later: %v", err)
	}
}
