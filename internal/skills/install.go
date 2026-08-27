package skills

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// Install scopes for the skill_install tool.
const (
	ScopeUser = "user" // ~/.agents/skills
	ScopeRepo = "repo" // <workBase>/.agents/skills
)

// Install clones a skill (git URL or local path) into the target
// scope, validates the resulting SKILL.md tree, and reloads the
// registry so the skill is usable immediately. subpath selects one
// skill directory inside the repo (e.g. "skills/flowcraft-config"),
// installing just that directory; empty installs the whole repo.
// Runs on the host: the sandbox cannot write user-level skill roots.
func (s *Service) Install(repo, scope, subpath string) (string, error) {
	if strings.TrimSpace(repo) == "" {
		return "", fmt.Errorf("skills: install repo is required")
	}
	if strings.HasPrefix(repo, "-") {
		return "", errdefs.Validationf(
			"skills: install repo must not start with '-'")
	}
	if scope == "" {
		scope = ScopeUser
	}
	target, err := s.installDir(scope)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	base := strings.TrimSuffix(filepath.Base(repo), ".git")
	if subpath != "" {
		base = filepath.Base(filepath.FromSlash(subpath))
	}
	name := slugify(base)
	if name == "" {
		name = "skill"
	}
	dst := filepath.Join(target, name)
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("skills: %q already exists at %s", name, dst)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("skills: git not found on host: %w", err)
	}
	tmp, err := os.MkdirTemp(target, ".skill-install-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// "--" ends option parsing so a repo that merely starts with "-"
	// (already rejected) or looks like a path can never be consumed as
	// a git option by a future caller.
	cmd := exec.CommandContext(
		ctx, git, "clone", "--depth", "1", "--", repo, tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("skills: git clone %q: %w: %s",
			repo, err, strings.TrimSpace(string(out)))
	}
	src := tmp
	if subpath != "" {
		src = filepath.Join(tmp, filepath.FromSlash(subpath))
		if err := ensureInside(tmp, src); err != nil {
			return "", err
		}
		if _, err := os.Stat(src); err != nil {
			return "", fmt.Errorf("skills: %q not found in repo %q", subpath, repo)
		}
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("skills: move %q to %q: %w", src, dst, err)
	}
	if _, err := validateTree(dst); err != nil {
		_ = os.RemoveAll(dst)
		return "", err
	}
	s.Reload()
	return dst, nil
}

// ensureInside verifies that child resolves inside parent — both
// lexically (filepath.Rel) and through symlinks (EvalSymlinks) — so a
// repo-supplied subpath cannot escape the clone directory via ".." or a
// symlink before it is moved into the skill root.
func ensureInside(parent, child string) error {
	contained := func(base, target string) bool {
		rel, err := filepath.Rel(base, target)
		return err == nil && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if !contained(parent, child) {
		return errdefs.Validationf(
			"skills: subpath %q escapes the clone directory", child)
	}
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		parentReal = parent
	}
	childReal, err := filepath.EvalSymlinks(child)
	if err != nil {
		childReal = filepath.Clean(child)
	}
	if !contained(parentReal, childReal) {
		return errdefs.Validationf(
			"skills: subpath %q resolves outside the clone directory", child)
	}
	return nil
}

// installDir resolves the target scope to an absolute skill root.
func (s *Service) installDir(scope string) (string, error) {
	switch scope {
	case ScopeRepo:
		if s.opts.WorkBase == "" {
			return "", fmt.Errorf("skills: install scope repo needs a work dir")
		}
		return filepath.Join(s.opts.WorkBase, ".agents", "skills"), nil
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("skills: resolve home for user scope: %w", err)
		}
		return filepath.Join(home, ".agents", "skills"), nil
	default:
		return "", fmt.Errorf("skills: unknown install scope %q (user | repo)", scope)
	}
}

// validateTree requires a valid skill in the clone: either a valid
// top-level SKILL.md (single-skill repo) or at least one valid
// SKILL.md below it (collection repo). Returns the valid skill names.
func validateTree(root string) ([]string, error) {
	if res, err := ParseFile(filepath.Join(root, "SKILL.md")); err == nil {
		return []string{res.Metadata.Name}, nil
	}
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "SKILL.md" {
			return err
		}
		if res, err := ParseFile(path); err == nil {
			names = append(names, res.Metadata.Name)
		}
		return nil
	})
	if len(names) == 0 {
		return nil, fmt.Errorf(
			"skills: cloned repo contains no valid SKILL.md (frontmatter needs name + description)")
	}
	return names, err
}
