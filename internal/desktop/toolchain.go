package desktop

import (
	"time"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/toolchain"
)

// currentToolchain returns the live toolchain manager from the
// assembled runtime, falling back to the startup default before a
// runtime is ready (MCP save/test can run without one).
func (a *App) currentToolchain() *toolchain.Manager {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.toolchainMgr != nil {
		return a.toolchainMgr
	}
	if a.toolchainFallback != nil {
		return a.toolchainFallback
	}
	// Tests and embedded hosts may construct App without New; a
	// default manager is harmless (external-only) and keeps
	// diagnostics/MCP helpers nil-safe.
	fallback, err := toolchain.New(toolchain.Options{
		Preference: toolchain.PreferenceExternalFirst,
	})
	if err == nil {
		a.toolchainFallback = fallback
		return fallback
	}
	return nil
}

// toolchainDiagnostics returns tool statuses, or an empty list when
// no manager could be constructed.
func (a *App) toolchainDiagnostics() []toolchain.RuntimeStatus {
	mgr := a.currentToolchain()
	if mgr == nil {
		return nil
	}
	statuses := mgr.Diagnose(a.appContext())
	for i := range statuses {
		if statuses[i].Error != "" || statuses[i].Path == "" {
			continue
		}
		args := []string{"--version"}
		if statuses[i].Family == "go" {
			args = []string{"version"}
		}
		statuses[i].Version = commandVersion(
			3*time.Second, statuses[i].Path, args...)
	}
	return statuses
}

// RuntimePreference returns the effective runtime_preference from the
// user configuration layer.
func (a *App) RuntimePreference() (string, error) {
	return config.LoadToolchainPreference(a.userDir)
}

// SaveRuntimePreference persists runtime_preference and rebuilds the
// runtime so the sandbox and MCP resolution pick it up.
func (a *App) SaveRuntimePreference(preference string) error {
	if err := config.WriteToolchainPreference(a.userDir, preference); err != nil {
		return err
	}
	return a.requestRebuild()
}

// ToolchainDiagnostics reports each managed tool's resolved version
// and source (system/bundled) for the diagnostics page.
func (a *App) ToolchainDiagnostics() ([]toolchain.RuntimeStatus, error) {
	return a.toolchainDiagnostics(), nil
}
