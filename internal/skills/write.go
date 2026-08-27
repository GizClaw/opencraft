package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	patchutil "github.com/GizClaw/opencraft/internal/utils/patch"

	"sigs.k8s.io/yaml"
)

// Skill authoring: skill_create / skill_modify tools write validated
// skill trees (SKILL.md plus supporting files: scripts, references,
// assets — including Python or Go sources) into the repo or user
// skill roots, and the desktop settings page deletes non-builtin
// skills through Delete. Every write goes through tmp+rename so a
// crash never leaves a truncated file, and the registry reloads so
// changes are immediately discoverable.

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
	if clean == "." ||
		filepath.IsAbs(clean) ||
		clean == ".." ||
		strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("skills: file path %q escapes the skill directory", rel)
	}
	if clean == "SKILL.md" {
		return "", fmt.Errorf(
			"skills: SKILL.md comes from name/description/body; remove it from files")
	}
	return clean, nil
}

// writeFileAtomic writes data to path via a temp file in the same
// directory followed by rename.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".skill-*.tmp")
	if err != nil {
		return fmt.Errorf("skills: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("skills: chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("skills: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("skills: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("skills: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("skills: rename temp file: %w", err)
	}
	return nil
}

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
// resolves the target scope root. Description is validated by Create
// only: Modify allows an empty description to keep the stored one.
func (s *Service) validateAuthoringArgs(name string, doc SkillDocument, scope string) (string, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return "", fmt.Errorf(
			"skills: invalid name %q (lowercase letters, digits and hyphens only)", name)
	}
	if strings.TrimSpace(doc.Body) == "" {
		return "", fmt.Errorf("skills: body is required")
	}
	if scope == "" {
		scope = ScopeRepo
	}
	root, err := s.installDir(scope)
	if err != nil {
		return "", err
	}
	return root, nil
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
// the target scope and reloads the registry so it is usable
// immediately. The skill must not already exist in that scope.
func (s *Service) Create(name string, doc SkillDocument, scope string) (string, error) {
	root, err := s.validateAuthoringArgs(name, doc, scope)
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
	defer func() { _ = os.RemoveAll(tmp) }()
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
// the target scope.
func (s *Service) Modify(name string, doc SkillDocument, scope string) (string, error) {
	root, err := s.validateAuthoringArgs(name, doc, scope)
	if err != nil {
		return "", err
	}
	if scope == "" {
		scope = ScopeRepo
	}
	name = strings.TrimSpace(name)
	dir := filepath.Join(root, name)
	path := filepath.Join(dir, "SKILL.md")
	existing, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("skills: %q not found in %s scope", name, scope)
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
func (s *Service) Patch(name, patch, scope string) ([]string, error) {
	if strings.TrimSpace(patch) == "" {
		return nil, fmt.Errorf("skills: patch is required")
	}
	dir, err := s.SkillDir(name, scope)
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(dir)
	tmp, err := os.MkdirTemp(root, ".skill-patch-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
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
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return nil, err
	}
	s.Reload()
	paths := make([]string, 0, len(results))
	for _, r := range results {
		paths = append(paths, r.Path)
	}
	return paths, nil
}

// SkillDir resolves the directory of one existing skill in scope.
func (s *Service) SkillDir(name, scope string) (string, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return "", fmt.Errorf(
			"skills: invalid name %q (lowercase letters, digits and hyphens only)", name)
	}
	if scope == "" {
		scope = ScopeRepo
	}
	root, err := s.installDir(scope)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, name)
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return "", fmt.Errorf("skills: %q not found in %s scope", name, scope)
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
// given SKILL.md path). Only paths inside the app's own writable
// skill roots are accepted; embedded builtin skills are refused.
func (s *Service) Delete(skillPath string) error {
	if strings.HasPrefix(skillPath, builtinPrefix) {
		return fmt.Errorf("skills: builtin skills cannot be deleted")
	}
	clean, err := filepath.Abs(filepath.Clean(skillPath))
	if err != nil {
		return fmt.Errorf("skills: resolve %q: %w", skillPath, err)
	}
	if filepath.Base(clean) != "SKILL.md" {
		return fmt.Errorf("skills: %q is not a SKILL.md path", skillPath)
	}
	dir := filepath.Dir(clean)
	inside := false
	for _, root := range s.writableRoots() {
		abs, err := filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		if dir == abs || strings.HasPrefix(dir, abs+string(os.PathSeparator)) {
			inside = true
			break
		}
	}
	if !inside {
		return fmt.Errorf("skills: %q is outside the writable skill roots", skillPath)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return fmt.Errorf("skills: %q does not exist", skillPath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("skills: %q is not a regular file", skillPath)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("skills: remove %q: %w", dir, err)
	}
	s.Reload()
	return nil
}

// writableRoots returns every skill root the app itself scans and may
// write: repo levels (root -> workBase), the user root, the user-dir
// skills root, and any configured extra roots.
func (s *Service) writableRoots() []string {
	var roots []string
	for _, dir := range repoLevels(s.opts.WorkBase) {
		roots = append(roots, filepath.Join(dir, ".agents", "skills"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".agents", "skills"))
	}
	if s.opts.UserDir != "" {
		roots = append(roots, filepath.Join(s.opts.UserDir, "skills"))
	}
	roots = append(roots, s.opts.ExtraRoots...)
	return roots
}
