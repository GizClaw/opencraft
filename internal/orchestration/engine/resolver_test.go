package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// TestOcraftResolverValues verifies the resolver maps the assembly
// path values the deploy document references, matching the retired
// OPEN_CRAFT_* process environment mapping (one value per name).
func TestOcraftResolverValues(t *testing.T) {
	layout := config.WorkspaceLayout{
		DataDir:       "/data",
		Root:          "/data/workspaces/w1",
		ApprovalsFile: "/data/workspaces/w1/approvals.yaml",
		SessionsDir:   "/data/workspaces/w1/sessions",
		CacheDir:      "/data/workspaces/w1/cache/tools",
		AuditDir:      "/data/workspaces/w1/audit",
	}
	o := Options{WorkBase: "/work", WorkspaceLayout: &layout}
	resolver := ocraftResolver(&o, "/data", "/data/cache")
	want := map[string]string{
		"WORKDIR":       "/work",
		"CACHE":         "/data/cache",
		"DATA_DIR":      "/data",
		"WORKSPACE_DIR": layout.Root,
		"SESSIONS_DIR":  layout.SessionsDir,
		"APPROVALS":     layout.ApprovalsFile,
		"TOOL_CACHE":    layout.CacheDir,
		"AUDIT_DIR":     layout.AuditDir,
	}
	for name := range want {
		got, err := resolver.Resolve(context.Background(), resource.Reference{
			Scheme: ocraftScheme,
			Path:   name,
		})
		if err != nil {
			t.Fatalf("resolve %s: %v", name, err)
		}
		if got != want[name] {
			t.Fatalf("resolve %s = %v, want %v", name, got, want[name])
		}
	}
	// Workspace-layout names are unresolvable without an injected
	// layout and fail closed instead of leaking host paths.
	o2 := Options{WorkBase: "/work"}
	resolver2 := ocraftResolver(&o2, "/data", "/data/cache")
	for _, name := range []string{"WORKSPACE_DIR", "SESSIONS_DIR", "APPROVALS"} {
		if _, err := resolver2.Resolve(context.Background(), resource.Reference{
			Scheme: ocraftScheme,
			Path:   name,
		}); err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("resolve %s without layout error = %v, want unknown reference", name, err)
		}
	}
}
