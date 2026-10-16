package skills

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/utils/fsatomic"
	"github.com/GizClaw/flowcraft/core/utils/pathsafe"
	"sigs.k8s.io/yaml"

	patchutil "github.com/GizClaw/opencraft/internal/foundation/utils/patch"
)

// Skill authoring: skill_create / skill_modify tools write validated
// skill trees (SKILL.md plus supporting files: scripts, references,
// assets — including Python or Go sources) into the user skill root
// (~/.agents/skills), and the desktop settings page deletes
// non-builtin skills through Delete. Every write goes through
// tmp+rename so a crash never leaves a truncated file, and the
// registry reloads so changes are immediately discoverable.

// SkillDocument is one authored skill payload: the SKILL.md body plus
// optional supporting files.
type SkillDocument struct {
	Description string // one-line description; empty keeps the stored one on Modify
	Body        string // SKILL.md markdown body (required)
	// Files maps a relative path inside the skill directory to its
	// content, e.g. "scripts/validate.sh" or
	// "scripts/validator/main.go". Directories are created as needed.
	Files map[string]string
	// Executable lists relative paths to chmod 0755 after writing
	// (shell/python scripts).
	Executable []string
}

// validRelPath validates one relative skill file path and rejects
// anything that would escape the skill directory.
func validRelPath(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("skills: empty file path")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || !pathsafe.RelRef(clean) {
		return "", fmt.Errorf("skills: file path %q escapes the skill directory", rel)
	}
	if clean == "SKILL.md" {
		return "", fmt.Errorf(
			"skills: SKILL.md comes from name/description/body; remove it from files")
	}
	return clean, nil
}

// writeFileAtomic writes data to path via a temp file in the same
// directory followed by rename (mechanics in fsatomic). The caller has
// already created the directory.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	return fsatomic.Write(path, data, fsatomic.Options{
		Perm:       perm,
		TempPrefix: ".skill-*.tmp",
		Sync:       true,
	})
}

// patchRename is split out so tests can inject a rename failure
// between the staged copy and the live skill directory.
var patchRename = os.Rename

// buildSkillDoc renders and validates one SKILL.md document: YAML
// frontmatter with name + description followed by the Markdown body.
func buildSkillDoc(name, description, body string) ([]byte, error) {
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	fm.Name = name
	fm.Description = description
	raw, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("skills: render frontmatter: %w", err)
	}
	doc := "---\n" + string(raw) + "---\n\n" + body
	if !strings.HasSuffix(doc, "\n") {
		doc += "\n"
	}
	data := []byte(doc)
	if _, err := parseBytes(filepath.Join(name, "SKILL.md"), data); err != nil {
		return nil, fmt.Errorf("skills: invalid skill: %w", err)
	}
	return data, nil
}

// validateAuthoringArgs checks the shared create/modify arguments and
// resolves the user skill root. Description is validated by Create
// only: Modify allows an empty description to keep the stored one.
func (s *Service) validateAuthoringArgs(name string, doc SkillDocument) (string, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return "", fmt.Errorf(
			"skills: invalid name %q (lowercase letters, digits and hyphens only)", name)
	}
	if strings.TrimSpace(doc.Body) == "" {
		return "", fmt.Errorf("skills: body is required")
	}
	return userSkillsDir()
}

// writeSupportFiles writes the supporting files of doc into dir and
// chmods the executable ones.
func writeSupportFiles(dir string, doc SkillDocument) error {
	for rel, content := range doc.Files {
		clean, err := validRelPath(rel)
		if err != nil {
			return err
		}
		fp := filepath.Join(dir, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(fp, []byte(content), 0o644); err != nil {
			return err
		}
	}
	for _, rel := range doc.Executable {
		clean, err := validRelPath(rel)
		if err != nil {
			return err
		}
		fp := filepath.Join(dir, filepath.FromSlash(clean))
		if err := os.Chmod(fp, 0o755); err != nil {
			return fmt.Errorf("skills: chmod %s: %w", clean, err)
		}
	}
	return nil
}

// Create writes a new skill tree (SKILL.md + supporting files) into
// the user skill root and reloads the registry so it is usable
// immediately. The skill must not already exist.
func (s *Service) Create(name string, doc SkillDocument) (string, error) {
	root, err := s.validateAuthoringArgs(name, doc)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(doc.Description) == "" {
		return "", fmt.Errorf("skills: description is required")
	}
	name = strings.TrimSpace(name)
	dst := filepath.Join(root, name)
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("skills: %q already exists at %s", filepath.Base(dst), dst)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	data, err := buildSkillDoc(
		name,
		sanitizeSingleLine(strings.TrimSpace(doc.Description)),
		doc.Body,
	)
	if err != nil {
		return "", err
	}
	// Stage in a temp sibling directory, then rename into place so a
	// failed write never leaves a half-created skill behind.
	tmp, err := os.MkdirTemp(root, ".skill-create-*")
	if err != nil {
		return "", err
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"skills: remove create temp failed", os.RemoveAll(tmp))
	}()
	if err := writeFileAtomic(filepath.Join(tmp, "SKILL.md"), data, 0o644); err != nil {
		return "", err
	}
	if err := writeSupportFiles(tmp, doc); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	s.Reload()
	return filepath.Join(dst, "SKILL.md"), nil
}

// Modify rewrites an existing skill's SKILL.md and upserts any
// supporting files, keeping the stored description when none is
// provided, and reloads the registry. The skill must already exist in
// the user skill root.
func (s *Service) Modify(name string, doc SkillDocument) (string, error) {
	root, err := s.validateAuthoringArgs(name, doc)
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	dir := filepath.Join(root, name)
	path := filepath.Join(dir, "SKILL.md")
	existing, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("skills: %q not found in the user skill root", name)
	}
	if strings.TrimSpace(doc.Description) == "" {
		parsed, err := parseBytes(path, existing)
		if err != nil {
			return "", fmt.Errorf("skills: existing skill is unreadable: %w", err)
		}
		doc.Description = parsed.Metadata.Description
	}
	data, err := buildSkillDoc(
		name,
		sanitizeSingleLine(strings.TrimSpace(doc.Description)),
		doc.Body,
	)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return "", err
	}
	if err := writeSupportFiles(dir, doc); err != nil {
		return "", err
	}
	s.Reload()
	return path, nil
}

// Patch applies a codex-format apply_patch document to one existing
// skill (SKILL.md and any supporting file), so an agent can edit just
// part of a skill instead of rewriting it wholesale. The patch runs
// against a staged copy of the skill, the result is re-validated, and
// the copy replaces the original only when everything succeeded.
// Returns the changed relative paths.
func (s *Service) Patch(name, patch string) ([]string, error) {
	if strings.TrimSpace(patch) == "" {
		return nil, fmt.Errorf("skills: patch is required")
	}
	dir, err := s.SkillDir(name)
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(dir)
	tmp, err := os.MkdirTemp(root, ".skill-patch-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"skills: remove patch temp failed", os.RemoveAll(tmp))
	}()
	if err := copyTree(dir, tmp); err != nil {
		return nil, fmt.Errorf("skills: stage skill for patch: %w", err)
	}
	results, err := patchutil.ApplyToDir(tmp, patch)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(tmp, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("skills: read patched SKILL.md: %w", err)
	}
	if _, err := parseBytes(filepath.Join(tmp, "SKILL.md"), data); err != nil {
		return nil, fmt.Errorf("skills: invalid skill after patch: %w", err)
	}
	// Swap via a sibling backup so a failed replacement never destroys
	// the original: move the live skill aside first, then rename the
	// staged copy into place, and only discard the backup afterwards.
	backup, err := os.MkdirTemp(root, ".skill-backup-*")
	if err != nil {
		return nil, fmt.Errorf("skills: create backup dir: %w", err)
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"skills: remove patch backup failed", os.RemoveAll(backup))
	}()
	if err := os.RemoveAll(backup); err != nil {
		return nil, fmt.Errorf("skills: prepare backup dir: %w", err)
	}
	if err := patchRename(dir, backup); err != nil {
		return nil, fmt.Errorf("skills: move current skill aside: %w", err)
	}
	if err := patchRename(tmp, dir); err != nil {
		if restoreErr := patchRename(backup, dir); restoreErr != nil {
			telemetry.WarnErr(context.Background(),
				"skills: restore original after patch replacement failure",
				restoreErr)
			return nil, fmt.Errorf(
				"skills: replace %q: %w (original restore failed: %v)",
				dir, err, restoreErr)
		}
		return nil, fmt.Errorf(
			"skills: replace %q: %w (original restored)", dir, err)
	}
	telemetry.WarnErr(context.Background(),
		"skills: remove patch backup after successful swap failed",
		os.RemoveAll(backup))
	s.Reload()
	paths := make([]string, 0, len(results))
	for _, r := range results {
		paths = append(paths, r.Path)
	}
	return paths, nil
}

// SkillDir resolves the directory of one existing user skill.
func (s *Service) SkillDir(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return "", fmt.Errorf(
			"skills: invalid name %q (lowercase letters, digits and hyphens only)", name)
	}
	root, err := userSkillsDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, name)
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return "", fmt.Errorf("skills: %q not found in the user skill root", name)
	}
	return dir, nil
}

// copyTree copies src into dst preserving directory/file modes and
// symlinks (used to stage a skill before patching).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chmod(target, info.Mode().Perm())
	})
}

// Delete removes one non-builtin skill directory (the parent of the
// given SKILL.md path). Only paths inside the app's own writable skill
// roots are accepted; embedded builtin skills are refused, and so are
// plugin-provided or extra-root skills, whose owner removes them.
//
// The containment decision runs on resolved paths: RemoveAll resolves a
// path's intermediate components when it executes, so a lexical
// comparison would accept a symlinked spelling whose target is the root
// itself, another tree, or a directory outside the roots entirely. The
// removal then goes through an os.Root handle pinned to the owning root,
// so even a link swapped in between the check and the delete cannot walk
// the removal back out of the tree.
func (s *Service) Delete(skillPath string) error {
	target, err := s.resolveDeleteTarget(skillPath)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(target.root, target.dir)
	if err != nil {
		return fmt.Errorf("skills: resolve %q under %q: %w",
			skillPath, target.root, err)
	}
	handle, err := os.OpenRoot(target.root)
	if err != nil {
		return fmt.Errorf("skills: open skill root %q: %w", target.root, err)
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"skills: close skill root handle failed", handle.Close())
	}()
	if err := handle.RemoveAll(rel); err != nil {
		return fmt.Errorf("skills: remove %q: %w", target.dir, err)
	}
	s.Reload()
	return nil
}

// Deletable reports whether Delete would accept this SKILL.md path. The
// settings page asks before it draws the delete affordance, so the
// button and the backend cannot disagree about which skills the app
// owns.
func (s *Service) Deletable(skillPath string) bool {
	_, err := s.resolveDeleteTarget(skillPath)
	return err == nil
}

// deleteTarget is the containment decision both Delete and Deletable
// run: resolveDeleteTarget turns one SKILL.md path into the resolved
// skill directory, the resolved document, and the app-owned root that
// contains them.
type deleteTarget struct {
	root string // the app-owned root containing the skill
	dir  string // the resolved skill directory to remove
	doc  string // the resolved SKILL.md
}

func (s *Service) resolveDeleteTarget(skillPath string) (deleteTarget, error) {
	if strings.HasPrefix(skillPath, builtinPrefix) {
		return deleteTarget{}, fmt.Errorf("skills: builtin skills cannot be deleted")
	}
	clean, err := filepath.Abs(filepath.Clean(skillPath))
	if err != nil {
		return deleteTarget{}, fmt.Errorf("skills: resolve %q: %w", skillPath, err)
	}
	if filepath.Base(clean) != "SKILL.md" {
		return deleteTarget{}, fmt.Errorf("skills: %q is not a SKILL.md path", skillPath)
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return deleteTarget{}, fmt.Errorf("skills: %q does not exist", skillPath)
	}
	dir := filepath.Dir(real)
	realRoot := ""
	for _, candidate := range s.writableRoots() {
		abs, err := filepath.Abs(filepath.Clean(candidate))
		if err != nil {
			continue
		}
		root, err := filepath.EvalSymlinks(abs)
		if err != nil {
			// A root that cannot be resolved serves nothing.
			continue
		}
		if dir == root {
			// A SKILL.md sitting directly in a scanned root is listed
			// as a skill; deleting "it" would RemoveAll the whole root
			// and every sibling skill with it.
			return deleteTarget{}, fmt.Errorf(
				"skills: %q sits directly in a skill root; refusing to delete the root itself",
				skillPath)
		}
		if pathsafe.Within(root, dir) {
			realRoot = root
			break
		}
	}
	if realRoot == "" {
		return deleteTarget{}, fmt.Errorf(
			"skills: %q is outside the app's own skill roots; "+
				"plugin skills go away with their plugin, and extra-root skills "+
				"are removed where they live",
			skillPath)
	}
	// The document has to be a real file, not a FIFO or a device named
	// SKILL.md: this check belongs to the same decision Delete makes, so
	// Deletable answers it too.
	info, err := os.Stat(real)
	if err != nil {
		return deleteTarget{}, fmt.Errorf("skills: %q does not exist", skillPath)
	}
	if !info.Mode().IsRegular() {
		return deleteTarget{}, fmt.Errorf("skills: %q is not a regular file", skillPath)
	}
	return deleteTarget{root: realRoot, dir: dir, doc: real}, nil
}

// writableRoots returns every skill root the app itself owns and may
// write: the user root and the per-user-dir skills root. Configured
// ExtraRoots (plugin-provided skills) are deliberately absent: those
// trees belong to the plugin Store, and a destructive write through
// this service would bypass its lifecycle. Discovery still scans them.
func (s *Service) writableRoots() []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".agents", "skills"))
	}
	if s.opts.UserDir != "" {
		roots = append(roots, filepath.Join(s.opts.UserDir, "skills"))
	}
	return roots
}
