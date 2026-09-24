package bindings

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	flowtelemetry "github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/execpolicy"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
	patchutil "github.com/GizClaw/opencraft/internal/foundation/utils/patch"
)

// Settings exposes per-conversation and sandbox settings.
type Settings struct {
	core *core.Core
}

// SessionDefaults is the mode/think level applied to conversations
// minted after the preference is saved.
type SessionDefaults struct {
	Mode  string `json:"mode"`
	Think string `json:"think"`
}

// NewSettingsBinding wires the settings binding.
func NewSettingsBinding(c *core.Core) *Settings {
	return &Settings{core: c}
}

// GetSessionDefaults returns the persisted new-session defaults.
func (b *Settings) GetSessionDefaults() SessionDefaults {
	mode, think := b.core.Shell.SessionDefaults()
	return SessionDefaults{Mode: mode, Think: think}
}

// SetSessionDefaults persists the new-session defaults and applies
// them to every conversation minted afterwards.
func (b *Settings) SetSessionDefaults(d SessionDefaults) error {
	mode := sessions.Mode(strings.TrimSpace(d.Mode))
	switch mode {
	case sessions.ModeWorkspace, sessions.ModeReadOnly, sessions.ModeYOLO:
	default:
		return fmt.Errorf("unknown permission mode %q", d.Mode)
	}
	// Defense in depth next to the sessions.Store guard: reject
	// confined defaults here so the preference document is never
	// rewritten with a mode the yoloonly build cannot honor.
	if profile.YoloOnly() && mode != sessions.ModeYOLO {
		return fmt.Errorf(
			"only yolo sandbox mode is available in this build")
	}
	think := sessions.ThinkLevel(strings.TrimSpace(d.Think))
	if !think.Valid() {
		return fmt.Errorf("unknown think level %q", d.Think)
	}
	if err := b.core.Shell.SetSessionDefaults(
		string(mode), string(think),
	); err != nil {
		return err
	}
	b.core.Conversation.SetDefaults(mode, string(think))
	return nil
}

// GetThink returns the current reasoning effort.
func (b *Settings) GetThink() (string, error) {
	return b.core.Conversation.Think(b.core.ActiveWorkDir()), nil
}

// SetThink updates and persists the reasoning effort.
func (b *Settings) SetThink(level string) error {
	ctx := b.core.Shell.Context()
	workDir := b.core.ActiveWorkDir()
	lv := sessions.ThinkLevel(level)
	if !lv.Valid() {
		return fmt.Errorf("unknown think level %q", level)
	}
	b.core.Conversation.SetThink(workDir, string(lv))
	if h := b.core.ActiveHost(); h != nil && h.Sessions() != nil {
		return h.Sessions().SetThink(
			ctx, b.core.Conversation.Current(workDir), lv,
		)
	}
	return nil
}

// GetModel returns the current model hint.
func (b *Settings) GetModel() (string, error) {
	return b.core.Conversation.Model(b.core.ActiveWorkDir()), nil
}

// SetModel updates and persists the model hint.
func (b *Settings) SetModel(model string) error {
	ctx := b.core.Shell.Context()
	workDir := b.core.ActiveWorkDir()
	model = strings.TrimSpace(model)
	b.core.Conversation.SetModel(workDir, model)
	if h := b.core.ActiveHost(); h != nil && h.Sessions() != nil {
		return h.Sessions().SetModel(
			ctx, b.core.Conversation.Current(workDir), model,
		)
	}
	return nil
}

// Permissions returns the current sandbox allowlist rules.
func (b *Settings) Permissions() ([]string, error) {
	mgr, err := b.execPolicy()
	if err != nil {
		// The settings page polls this before the runtime exists.
		return []string{}, nil
	}
	return mgr.Rules(), nil
}

// AllowPermission adds one sandbox allowlist rule.
func (b *Settings) AllowPermission(rule string) error {
	mgr, err := b.execPolicy()
	if err != nil {
		return err
	}
	return mgr.AlwaysAllow(strings.TrimSpace(rule))
}

// DenyPermission removes one sandbox allowlist rule.
func (b *Settings) DenyPermission(rule string) error {
	mgr, err := b.execPolicy()
	if err != nil {
		return err
	}
	return mgr.Remove(strings.TrimSpace(rule))
}

// EscalatedPermissions returns the commands the user allowed to run
// outside the sandbox ("always" answers to an escalation prompt).
func (b *Settings) EscalatedPermissions() ([]string, error) {
	mgr, err := b.execPolicy()
	if err != nil {
		// Mirror Permissions: page load before the runtime is ready.
		return []string{}, nil
	}
	return mgr.EscalatedRules(), nil
}

// AllowEscalatedPermission adds one "run outside the sandbox" rule.
func (b *Settings) AllowEscalatedPermission(rule string) error {
	mgr, err := b.execPolicy()
	if err != nil {
		return err
	}
	return mgr.AlwaysEscalate(strings.TrimSpace(rule))
}

// DenyEscalatedPermission removes one "run outside the sandbox" rule.
func (b *Settings) DenyEscalatedPermission(rule string) error {
	mgr, err := b.execPolicy()
	if err != nil {
		return err
	}
	return mgr.RemoveEscalated(strings.TrimSpace(rule))
}

// execPolicy resolves the shared exec policy manager from the current
// runtime.
func (b *Settings) execPolicy() (*execpolicy.Manager, error) {
	h := b.core.ActiveHost()
	if h == nil || h.Controller() == nil || h.Controller().Runtime() == nil {
		return nil, errors.New("settings: runtime is not ready")
	}
	value, ok := h.Controller().Runtime().Resource("execpolicy")
	if !ok {
		return nil, errors.New("settings: execpolicy resource is not wired")
	}
	mgr, ok := value.(*execpolicy.Manager)
	if !ok {
		return nil, errors.New(
			"settings: execpolicy resource has an unexpected type")
	}
	return mgr, nil
}

func (b *Settings) skillsService() (*skills.Service, error) {
	h := b.core.ActiveHost()
	if h == nil || h.Controller() == nil || h.Controller().Runtime() == nil {
		return nil, errors.New("settings: runtime is not ready")
	}
	value, ok := h.Controller().Runtime().Resource("skills")
	if !ok {
		return nil, errors.New("settings: skills resource is not available")
	}
	svc, ok := value.(*skills.Service)
	if !ok || svc == nil {
		return nil, errors.New("settings: skills resource is not available")
	}
	return svc, nil
}

// SkillSummary is the settings-page view of one skill.
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope,omitempty"`
	Path        string `json:"path"`
	PluginID    string `json:"plugin_id,omitempty"`
	PluginName  string `json:"plugin_name,omitempty"`
}

// Skills returns the discovered skill registry.
func (b *Settings) Skills() ([]SkillSummary, error) {
	svc, err := b.skillsService()
	if err != nil {
		return []SkillSummary{}, nil
	}
	items := svc.List()
	out := make([]SkillSummary, 0, len(items))
	for _, s := range items {
		summary := SkillSummary{
			Name:        s.Name,
			Description: s.Description,
			Scope:       s.Scope,
			Path:        s.Path,
		}
		if pluginID, pluginName, ok := pluginOwnerForSkillPath(b.core, s.Path); ok {
			summary.PluginID = pluginID
			summary.PluginName = pluginName
		}
		out = append(out, summary)
	}
	return out, nil
}

// SkillContent returns the body of one discovered SKILL.md.
func (b *Settings) SkillContent(skillPath string) (string, error) {
	svc, err := b.skillsService()
	if err != nil {
		return "", err
	}
	_, body, err := svc.ReadByPath(skillPath)
	return body, err
}

// DeleteSkill removes one non-builtin skill.
func (b *Settings) DeleteSkill(skillPath string) error {
	svc, err := b.skillsService()
	if err != nil {
		return err
	}
	return svc.Delete(skillPath)
}

// InstallSkill clones a git skill into the user skill root.
func (b *Settings) InstallSkill(
	repo, subpath string,
) (string, error) {
	ctx := b.core.Shell.Context()
	svc, err := b.skillsService()
	if err != nil {
		return "", err
	}
	return svc.Install(ctx, repo, subpath)
}

// RenderSkillPatch renders a codex patch against one user skill
// directory.
func (b *Settings) RenderSkillPatch(
	name, patch string,
) ([]PatchFile, error) {
	svc, err := b.skillsService()
	if err != nil {
		return nil, err
	}
	dir, err := svc.SkillDir(name)
	if err != nil {
		return nil, err
	}
	files, err := patchutil.Diff(patch, func(path string) (string, error) {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		return string(data), err
	})
	if err != nil {
		return nil, err
	}
	out := make([]PatchFile, 0, len(files))
	for _, f := range files {
		pf := PatchFile{
			Path:    f.Path,
			Action:  f.Action,
			Added:   f.Added,
			Removed: f.Removed,
			Lines:   []PatchLine{},
		}
		for _, l := range f.Lines {
			kind := "context"
			switch l.Kind {
			case patchutil.DiffLineAdd:
				kind = "add"
			case patchutil.DiffLineDelete:
				kind = "delete"
			}
			pf.Lines = append(pf.Lines, PatchLine{
				Kind: kind, OldNum: l.OldNum, NewNum: l.NewNum, Text: l.Text,
			})
		}
		out = append(out, pf)
	}
	return out, nil
}

// ReadLog returns the tail of the app log file.
//
// The log is multi-megabyte after a long session and the diagnostics
// viewer refreshes on demand, so the tail is read by seeking from the end
// instead of loading the whole file: a window is read, and grown only
// when it turned out to hold fewer lines than requested.
func (b *Settings) ReadLog(n int) (string, error) {
	if n <= 0 {
		n = 200
	}
	path := filepath.Join(b.core.DataDir, "logs", "opencraft.log")
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		flowtelemetry.WarnErr(b.core.Shell.Context(),
			"settings: close log file failed", f.Close())
	}()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()
	// maxWindow bounds one read even when the caller asks for a very long
	// tail; 64 KiB per requested line is a generous average line estimate.
	const minWindow = 64 << 10
	maxWindow := int64(n) * 64 << 10
	if maxWindow < minWindow {
		maxWindow = minWindow
	}
	window := int64(minWindow)
	for {
		if window > size {
			window = size
		}
		if window > maxWindow {
			window = maxWindow
		}
		buf := make([]byte, window)
		if _, err := f.ReadAt(buf, size-window); err != nil &&
			!errors.Is(err, io.EOF) {
			return "", err
		}
		reachedStart := window == size
		body := strings.TrimRight(string(buf), "\n")
		lines := []string{}
		if body != "" {
			lines = strings.Split(body, "\n")
		}
		if reachedStart || len(lines) > n || window >= maxWindow {
			if !reachedStart && len(lines) > n {
				// The window starts mid-line; that fragment is not a real
				// first line, so drop it before keeping the tail.
				lines = lines[1:]
			}
			if len(lines) > n {
				lines = lines[len(lines)-n:]
			}
			return strings.Join(lines, "\n"), nil
		}
		window *= 4
	}
}
