package desktop

// Thin wails bindings over internal/plugins. All plugin logic lives in
// the plugins package; these methods only connect it to the app state.

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/GizClaw/opencraft/internal/plugins"
	pluginupdate "github.com/GizClaw/opencraft/internal/plugins/update"
	"github.com/GizClaw/opencraft/internal/skills"
)

// PluginList returns every installed plugin.
func (a *App) PluginList() ([]plugins.PluginSummary, error) {
	if a.plugins == nil {
		return nil, errors.New("plugin store is not ready")
	}
	return a.plugins.List()
}

// PluginBundle returns the plugin entry bundle source.
func (a *App) PluginBundle(id string) (string, error) {
	if a.plugins == nil {
		return "", errors.New("plugin store is not ready")
	}
	return a.plugins.Bundle(id)
}

// PluginSetEnabled toggles a plugin's enabled state.
func (a *App) PluginSetEnabled(id string, enabled bool) error {
	if a.plugins == nil {
		return errors.New("plugin store is not ready")
	}
	if err := a.plugins.SetEnabled(id, enabled); err != nil {
		return err
	}
	if !enabled && a.cap != nil {
		a.cap.Stop(id)
	}
	// Agent-facing capabilities (skills / MCP / hooks / tools) are
	// assembled into the runtime; refresh it so the change takes
	// effect (immediately, or after the current turn when one runs).
	return a.refreshAgentPlugins()
}

// PluginInstall copies a plugin folder into the plugin root.
func (a *App) PluginInstall(src string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	sum, err := a.plugins.Install(src)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginInstallZip installs a plugin from a zip package (a release
// artifact). The archive is extracted safely and installed through the
// normal Install path.
func (a *App) PluginInstallZip(zipPath string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	sum, err := a.plugins.InstallZip(zipPath)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginInspect reads a plugin source folder/zip and reports its
// manifest summary (including whether it would shadow a builtin)
// without installing it. The install dialog uses it for pre-flight
// warnings.
func (a *App) PluginInspect(src string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	return a.plugins.Inspect(src)
}

// PluginToolDTO is the UI-facing view of one agent-callable tool
// declared by a plugin manifest.
type PluginToolDTO struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Method       string `json:"method"`
	MutatesState bool   `json:"mutates_state"`
}

// PluginTools returns the agent-callable tools a plugin declares. The
// plugin manager uses it to render an expandable tool list; the tools
// themselves are only callable by the agent through the tool catalog.
func (a *App) PluginTools(id string) ([]PluginToolDTO, error) {
	if a.plugins == nil {
		return nil, errors.New("plugin store is not ready")
	}
	m, err := a.plugins.Manifest(id)
	if err != nil {
		return nil, err
	}
	out := make([]PluginToolDTO, 0, len(m.Tools))
	for _, t := range m.Tools {
		mutates := true
		if t.MutatesState != nil {
			mutates = *t.MutatesState
		}
		out = append(out, PluginToolDTO{
			Name:         t.Name,
			Description:  t.Description,
			Method:       t.Method,
			MutatesState: mutates,
		})
	}
	return out, nil
}

// PluginSkills returns the skills discovered under a plugin's skill
// roots. The plugin manager renders them as an expandable list; the
// skills themselves are served through the shared skills registry.
func (a *App) PluginSkills(id string) ([]SkillDTO, error) {
	if a.plugins == nil {
		return nil, errors.New("plugin store is not ready")
	}
	m, err := a.plugins.Manifest(id)
	if err != nil {
		return nil, err
	}
	dir, _, err := a.plugins.Dir(id)
	if err != nil {
		return nil, err
	}
	var roots []string
	if len(m.Skills) == 0 {
		roots = []string{filepath.Join(dir, "skills")}
	} else {
		for _, rel := range m.Skills {
			if abs, ok := pluginPathInside(dir, rel); ok {
				roots = append(roots, abs)
			}
		}
	}
	// The shared skills registry is the source of truth when the
	// runtime is assembled; the direct scan is a fallback for
	// settings-only sessions or a stale/disabled registry. Merging
	// both also keeps the plugin manager usable before the runtime is
	// ready.
	out := make([]SkillDTO, 0, 4)
	seen := map[string]bool{}
	svc, svcErr := a.skillsService()
	if svcErr == nil {
		items := svc.List()
		for _, s := range items {
			if !skillInAnyRoot(s.Path, roots) {
				continue
			}
			seen[s.Path] = true
			out = append(out, SkillDTO{
				Name:        s.Name,
				Description: s.Description,
				Scope:       s.Scope,
				Path:        s.Path,
				PluginID:    id,
				PluginName:  m.Name,
			})
		}
	}
	for _, s := range scanPluginSkillRoots(roots) {
		if seen[s.Path] {
			continue
		}
		s.PluginID = id
		s.PluginName = m.Name
		seen[s.Path] = true
		out = append(out, s)
	}
	return out, nil
}

// pluginPathInside resolves a manifest-relative path inside the plugin
// directory, rejecting lexical escapes (mirrors plugins.resolveInside).
func pluginPathInside(dir, rel string) (string, bool) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) ||
		clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(dir, clean), true
}

// skillInAnyRoot reports whether an absolute skill path lives under
// one of the plugin's skill roots.
func skillInAnyRoot(skillPath string, roots []string) bool {
	for _, root := range roots {
		root = filepath.Clean(root)
		if skillPath == root ||
			strings.HasPrefix(skillPath, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// scanPluginSkillRoots walks a plugin's declared skill roots and parses
// every SKILL.md without depending on the assembled runtime. Used as a
// fallback so the plugin manager can list skills before the runtime is
// ready.
func scanPluginSkillRoots(roots []string) []SkillDTO {
	var out []SkillDTO
	seen := map[string]bool{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			// Never follow symlinks in the direct fallback scan: the
			// shared skills registry resolves and contains them, but
			// this path is a settings-only convenience and must not
			// parse files outside the plugin's declared skill roots.
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				// Only skip hidden directories below the scan root;
				// the root itself may legitimately live under a hidden
				// path such as ~/.opencraft/plugins.
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() != "SKILL.md" {
				return nil
			}
			clean := filepath.Clean(path)
			if seen[clean] {
				return nil
			}
			res, parseErr := skills.ParseFile(clean)
			if parseErr != nil {
				return nil
			}
			seen[clean] = true
			out = append(out, SkillDTO{
				Name:        res.Metadata.Name,
				Description: res.Metadata.Description,
				Scope:       "user",
				Path:        clean,
			})
			return nil
		})
	}
	return out
}

// pluginSkillOwner is the plugin that contributes one skill root.
type pluginSkillOwner struct {
	ID   string
	Name string
}

// pluginSkillRootOwners maps each installed plugin's skill roots to
// the plugin that owns them. The skills page uses this to mark plugin
// skills as read-only and show the owning plugin, even when the plugin
// is currently disabled.
func (a *App) pluginSkillRootOwners() map[string]pluginSkillOwner {
	if a.plugins == nil {
		return nil
	}
	list, err := a.plugins.List()
	if err != nil {
		return nil
	}
	owners := map[string]pluginSkillOwner{}
	for _, p := range list {
		if p.Error != "" ||
			!pluginHasPermission(p.Permissions, "skills:contribute") {
			continue
		}
		m, err := a.plugins.Manifest(p.ID)
		if err != nil {
			continue
		}
		dir, _, err := a.plugins.Dir(p.ID)
		if err != nil {
			continue
		}
		var roots []string
		if len(m.Skills) == 0 {
			roots = []string{filepath.Join(dir, "skills")}
		} else {
			for _, rel := range m.Skills {
				if abs, ok := pluginPathInside(dir, rel); ok {
					roots = append(roots, abs)
				}
			}
		}
		owner := pluginSkillOwner{ID: p.ID, Name: p.Name}
		for _, root := range roots {
			owners[filepath.Clean(root)] = owner
		}
	}
	return owners
}

// pluginSkillOwnerForPath resolves the plugin that owns a skill path,
// if any.
func pluginSkillOwnerForPath(
	owners map[string]pluginSkillOwner,
	skillPath string,
) (pluginSkillOwner, bool) {
	for root, owner := range owners {
		if skillPath == root ||
			strings.HasPrefix(skillPath, root+string(filepath.Separator)) {
			return owner, true
		}
	}
	return pluginSkillOwner{}, false
}

func pluginHasPermission(perms []string, want string) bool {
	for _, p := range perms {
		if p == want {
			return true
		}
	}
	return false
}

// PluginUpdate replaces an installed plugin with a newer version from
// a local directory. The previous version is kept for rollback.
func (a *App) PluginUpdate(id, src string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	sum, err := a.plugins.Update(id, src)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if a.cap != nil {
		a.cap.Stop(id)
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginUpdateZip updates a plugin from a zip package.
func (a *App) PluginUpdateZip(id, zipPath string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	sum, err := a.plugins.UpdateZip(id, zipPath)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if a.cap != nil {
		a.cap.Stop(id)
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginRollback restores the previous version snapshot of a plugin.
func (a *App) PluginRollback(id string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	sum, err := a.plugins.Rollback(id)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if a.cap != nil {
		a.cap.Stop(id)
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginCheckUpdate fetches the plugin's declared update manifest and
// returns the newest version metadata without applying anything.
func (a *App) PluginCheckUpdate(id string) (plugins.UpdateInfo, error) {
	if a.plugins == nil {
		return plugins.UpdateInfo{}, errors.New("plugin store is not ready")
	}
	m, err := a.plugins.Manifest(id)
	if err != nil {
		return plugins.UpdateInfo{}, err
	}
	if m.Update == nil {
		return plugins.UpdateInfo{}, errors.New(
			"plugin does not declare an update url")
	}
	return pluginupdate.Check(a.appContext(), m.Update.URL)
}

// PluginApplyUpdate checks the update manifest, downloads and verifies
// the package, then applies it through the normal update pipeline
// (version constraint, resource validation, rollback snapshot).
func (a *App) PluginApplyUpdate(id string) (plugins.PluginSummary, error) {
	if a.plugins == nil {
		return plugins.PluginSummary{}, errors.New("plugin store is not ready")
	}
	m, err := a.plugins.Manifest(id)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if m.Update == nil {
		return plugins.PluginSummary{}, errors.New(
			"plugin does not declare an update url")
	}
	info, err := pluginupdate.Check(a.appContext(), m.Update.URL)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	zipPath, cleanup, err := pluginupdate.FetchZip(
		a.appContext(), info, pluginupdate.Policy{})
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	defer cleanup()
	_, builtin, err := a.plugins.Dir(id)
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	var sum plugins.PluginSummary
	if builtin {
		// The builtin bundle is read-only: applying a remote update
		// installs the package as a user-root shadow copy. The builtin
		// stays intact and reappears if the shadow is uninstalled.
		sum, err = a.plugins.InstallZip(zipPath)
	} else {
		sum, err = a.plugins.UpdateZip(id, zipPath)
	}
	if err != nil {
		return plugins.PluginSummary{}, err
	}
	if a.cap != nil {
		a.cap.Stop(id)
	}
	if err := a.refreshAgentPlugins(); err != nil {
		return plugins.PluginSummary{}, err
	}
	return sum, nil
}

// PluginUninstall removes a plugin and its KV data.
func (a *App) PluginUninstall(id string) error {
	if a.plugins == nil {
		return errors.New("plugin store is not ready")
	}
	// Ask the capability plugin to clean up its own resources first
	// (inference profile, secrets): the plugin knows what it wrote.
	if a.cap != nil {
		// Best-effort: the host fallback below still removes leftovers.
		_ = a.cap.Cleanup(id)
	}
	// Host fallback: a plugin may not implement cleanup or may have
	// written resources earlier; remove lingering inference config and
	// secrets defensively so nothing survives the uninstall.
	profileRemoved := false
	if err := a.removeInferenceProfile(id); err == nil {
		profileRemoved = true
	}
	if a.secrets != nil {
		_ = a.secrets.DeletePrefix(a.appContext(), "auth/"+id+"/")
	}
	a.kv.RemoveAll(id)
	if err := a.plugins.Uninstall(id); err != nil {
		return err
	}
	if a.cap != nil {
		a.cap.Stop(id)
	}
	// Notify before rebuild so the settings page reflects the config
	// change even if the rebuild fails.
	if profileRemoved && a.bridge != nil {
		a.bridge.Emit("inference_changed", map[string]any{})
	}
	return a.refreshAgentPlugins()
}
