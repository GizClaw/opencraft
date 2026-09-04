package bindings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/delegation/kanban"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/execpolicy"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	patchutil "github.com/GizClaw/opencraft/internal/foundation/utils/patch"
)

// Settings exposes per-conversation and sandbox settings.
type Settings struct {
	core *core.Core
}

// NewSettingsBinding wires the settings binding.
func NewSettingsBinding(c *core.Core) *Settings {
	return &Settings{core: c}
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
	if h := b.core.Runtime.Current(); h != nil && h.Sessions() != nil {
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
	if h := b.core.Runtime.Current(); h != nil && h.Sessions() != nil {
		return h.Sessions().SetModel(
			ctx, b.core.Conversation.Current(workDir), model,
		)
	}
	return nil
}

// Permissions returns the current sandbox allowlist rules.
func (b *Settings) Permissions() ([]string, error) {
	h := b.core.Runtime.Current()
	if h == nil || h.Controller() == nil || h.Controller().Runtime() == nil {
		return []string{}, nil
	}
	value, ok := h.Controller().Runtime().Resource("execpolicy")
	if !ok {
		return []string{}, nil
	}
	mgr, ok := value.(*execpolicy.Manager)
	if !ok || mgr == nil {
		return []string{}, nil
	}
	return mgr.Rules(), nil
}

// AllowPermission adds one sandbox allowlist rule.
func (b *Settings) AllowPermission(rule string) error {
	h := b.core.Runtime.Current()
	if h == nil || h.Controller() == nil || h.Controller().Runtime() == nil {
		return errors.New("settings: runtime is not ready")
	}
	value, ok := h.Controller().Runtime().Resource("execpolicy")
	if !ok {
		return errors.New("settings: execpolicy resource is not wired")
	}
	mgr, ok := value.(*execpolicy.Manager)
	if !ok {
		return errors.New("settings: execpolicy resource has an unexpected type")
	}
	return mgr.AlwaysAllow(strings.TrimSpace(rule))
}

// DenyPermission removes one sandbox allowlist rule.
func (b *Settings) DenyPermission(rule string) error {
	h := b.core.Runtime.Current()
	if h == nil || h.Controller() == nil || h.Controller().Runtime() == nil {
		return errors.New("settings: runtime is not ready")
	}
	value, ok := h.Controller().Runtime().Resource("execpolicy")
	if !ok {
		return errors.New("settings: execpolicy resource is not wired")
	}
	mgr, ok := value.(*execpolicy.Manager)
	if !ok {
		return errors.New("settings: execpolicy resource has an unexpected type")
	}
	return mgr.Remove(strings.TrimSpace(rule))
}

func (b *Settings) skillsService() (*skills.Service, error) {
	h := b.core.Runtime.Current()
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

// InstallSkill clones a git skill into scope.
func (b *Settings) InstallSkill(
	repo, scope, subpath string,
) (string, error) {
	ctx := b.core.Shell.Context()
	svc, err := b.skillsService()
	if err != nil {
		return "", err
	}
	return svc.Install(ctx, repo, scope, subpath)
}

// RenderSkillPatch renders a codex patch against one skill directory.
func (b *Settings) RenderSkillPatch(
	name, scope, patch string,
) ([]PatchFile, error) {
	svc, err := b.skillsService()
	if err != nil {
		return nil, err
	}
	dir, err := svc.SkillDir(name, scope)
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
func (b *Settings) ReadLog(n int) (string, error) {
	if n <= 0 {
		n = 200
	}
	data, err := os.ReadFile(
		filepath.Join(b.core.DataDir, "logs", "opencraft.log"),
	)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

// CancelCard cancels one delegation board card.
func (b *Settings) CancelCard(id string) (bool, error) {
	h := b.core.Runtime.Current()
	if h == nil || h.Controller() == nil || h.Controller().Runtime() == nil {
		return false, errors.New("settings: runtime is not ready")
	}
	value, ok := h.Controller().Runtime().Resource("delegate.backend")
	if !ok {
		return false, errors.New("settings: delegation backend is not wired")
	}
	board, ok := value.(*kanban.Board)
	if !ok {
		return false, errors.New("settings: delegation backend has an unexpected type")
	}
	return board.Cancel(id, "cancelled from the desktop board"), nil
}
