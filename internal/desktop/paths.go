package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// snapshotWorkDir returns the current workspace directory under the
// app lock; callers must not hold a.mu themselves.
func (a *App) snapshotWorkDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workDir
}

// resolveInWorkspace resolves p against the workspace root and refuses
// paths that escape it — including via symlinks. Relative paths are
// resolved from the root; absolute paths are allowed only when they
// stay inside. The returned path is symlink-resolved so the caller
// operates on the real location.
func resolveInWorkspace(workDir, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is required")
	}
	root, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	candidate := p
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)

	resolved, err := evalLongestExisting(candidate)
	if err != nil {
		return "", err
	}
	rootResolved, err := evalLongestExisting(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	if resolved != rootResolved &&
		!strings.HasPrefix(resolved, rootResolved+string(os.PathSeparator)) {
		return "", fmt.Errorf("%s is outside the workspace", p)
	}
	return resolved, nil
}

// evalLongestExisting resolves symlinks on the longest existing prefix
// of p and re-appends the remaining components, so paths whose final
// component was just deleted (e.g. a git diff of a removed file) still
// resolve for containment checks.
func evalLongestExisting(p string) (string, error) {
	var tail []string
	cur := filepath.Clean(p)
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("cannot resolve %s: %w", p, err)
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}
