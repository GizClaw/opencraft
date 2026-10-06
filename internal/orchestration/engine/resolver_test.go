package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
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

// TestOcraftResolverValuesForAnApplication pins the application half of
// the resolver table: every state value that can be per-scope points
// inside the application's own root — the private workspace included —
// while the shared roots (DATA_DIR, APP_HOME, the build cache) keep
// their process-wide values. The content root is deliberately not a
// resolvable name at all: layers reach it through FileBase, which the
// document cannot ask for.
func TestOcraftResolverValuesForAnApplication(t *testing.T) {
	dataDir := t.TempDir()
	layout, err := config.AppLayout(dataDir, "hello")
	if err != nil {
		t.Fatalf("AppLayout: %v", err)
	}
	appHome := filepath.Join(dataDir, "home")
	o := Options{
		WorkBase:        layout.WorkDir,
		AppHome:         appHome,
		FileBase:        filepath.Join(dataDir, "content"),
		WorkspaceLayout: &layout,
	}
	cacheDir := filepath.Join(dataDir, "cache")
	resolver := ocraftResolver(&o, dataDir, cacheDir)
	want := map[string]string{
		"WORKDIR":       layout.WorkDir,
		"WORKSPACE_DIR": layout.Root,
		"SESSIONS_DIR":  layout.SessionsDir,
		"APPROVALS":     layout.ApprovalsFile,
		"TOOL_CACHE":    layout.CacheDir,
		"AUDIT_DIR":     layout.AuditDir,
		"DATA_DIR":      dataDir,
		"APP_HOME":      appHome,
		"CACHE":         cacheDir,
	}
	for name, value := range want {
		got, err := resolver.Resolve(context.Background(), resource.Reference{
			Scheme: ocraftScheme,
			Path:   name,
		})
		if err != nil {
			t.Fatalf("resolve %s: %v", name, err)
		}
		if got != value {
			t.Errorf("resolve %s = %v, want %v", name, got, value)
		}
	}
	for name, value := range want {
		switch name {
		case "DATA_DIR", "APP_HOME", "CACHE":
			continue
		}
		if !pathsafe.Within(layout.Root, value) {
			t.Errorf("%s = %v, want a path inside the application root %s",
				name, value, layout.Root)
		}
	}
}
