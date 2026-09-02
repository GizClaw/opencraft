package config

import (
	"testing"

	"github.com/GizClaw/flowcraft/core/deploy"
	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/toolchain"
)

func TestMigrateMCPToolchainUpgradesLegacyImpl(t *testing.T) {
	doc := deploy.Document{
		Resources: resource.Resources{
			"tool.mcp": {
				Kind: "tool.Source",
				Impl: "mcp",
			},
		},
	}
	if err := MigrateMCPToolchain(&doc); err != nil {
		t.Fatal(err)
	}
	res := doc.Resources["tool.mcp"]
	if res.Impl != toolchain.MCPResourceImpl {
		t.Fatalf("impl = %q, want %q", res.Impl, toolchain.MCPResourceImpl)
	}
	if res.Deps["toolchain"] != resource.Ref("toolchain") {
		t.Fatalf("toolchain dep = %q, want toolchain", res.Deps["toolchain"])
	}
}

func TestMigrateMCPToolchainLeavesCurrentAndOtherResources(t *testing.T) {
	doc := deploy.Document{
		Resources: resource.Resources{
			"tool.mcp": {
				Kind: "tool.Source",
				Impl: toolchain.MCPResourceImpl,
				Deps: resource.Deps{"toolchain": "toolchain"},
			},
			"box": {
				Kind: "sandbox.Runner",
				Impl: "opencraft",
			},
		},
	}
	if err := MigrateMCPToolchain(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Resources["tool.mcp"].Impl != toolchain.MCPResourceImpl {
		t.Fatal("current impl must stay untouched")
	}
	if _, ok := doc.Resources["box"]; !ok {
		t.Fatal("unrelated resource must be preserved")
	}
}
