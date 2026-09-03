package desktop

import (
	"errors"
	"os"

	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	patchutil "github.com/GizClaw/opencraft/internal/foundation/utils/patch"
)

// RenderPatch returns a git-style diff (with line numbers) for a
// workspace-relative apply_patch document, computed against the
// current workspace files, so the chat can render the patch the way
// git diff would.
func (a *App) RenderPatch(patch string) ([]PatchFileDTO, error) {
	return renderPatchDiff(a.snapshotWorkDir(), patch)
}

// RenderSkillPatch returns a git-style diff for a codex patch applied
// to one skill's files (skill_modify), resolved against the skill
// directory in its scope.
func (a *App) RenderSkillPatch(name, scope, patch string) ([]PatchFileDTO, error) {
	svc, err := a.skillsService()
	if err != nil {
		return nil, err
	}
	dir, err := svc.SkillDir(name, scope)
	if err != nil {
		return nil, err
	}
	return renderPatchDiff(dir, patch)
}

func (a *App) skillsService() (*skills.Service, error) {
	ctrl := a.controller()
	if ctrl == nil || ctrl.Runtime() == nil {
		return nil, errors.New("runtime is not ready")
	}
	value, ok := ctrl.Runtime().Resource("skills")
	if !ok {
		return nil, errors.New("skills resource is not available")
	}
	svc, ok := value.(*skills.Service)
	if !ok {
		return nil, errors.New("skills resource is not available")
	}
	return svc, nil
}

// renderPatchDiff renders one codex patch against files under root.
func renderPatchDiff(root, patch string) ([]PatchFileDTO, error) {
	readFile := func(path string) (string, error) {
		resolved, err := resolveInWorkspace(root, path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	files, err := patchutil.Diff(patch, readFile)
	if err != nil {
		return nil, err
	}
	out := make([]PatchFileDTO, 0, len(files))
	for _, f := range files {
		dto := PatchFileDTO{
			Path:    f.Path,
			Action:  f.Action,
			Added:   f.Added,
			Removed: f.Removed,
		}
		for _, l := range f.Lines {
			kind := "context"
			switch l.Kind {
			case patchutil.DiffLineAdd:
				kind = "add"
			case patchutil.DiffLineDelete:
				kind = "delete"
			}
			dto.Lines = append(dto.Lines, PatchLineDTO{
				Kind:   kind,
				OldNum: l.OldNum,
				NewNum: l.NewNum,
				Text:   l.Text,
			})
		}
		out = append(out, dto)
	}
	return out, nil
}
