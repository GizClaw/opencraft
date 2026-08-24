package patch

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// ApplyToDir applies a codex-format patch to files under dir on the
// host filesystem. Every operation path must stay inside dir:
// absolute paths, "..", and symlink escapes are rejected, so a patch
// can never touch files outside the target directory.
func ApplyToDir(dir, patch string) ([]FileResult, error) {
	ops, err := Parse(patch)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	insideRoot := func(p string) bool {
		return p == rootResolved ||
			strings.HasPrefix(p, rootResolved+string(os.PathSeparator))
	}

	var results []FileResult
	for _, op := range ops {
		if op == nil {
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(op.path))
		if filepath.IsAbs(clean) ||
			clean == ".." ||
			strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return results, errdefs.Validationf(
				"apply_patch: path %q escapes %s", op.path, dir)
		}
		fp := filepath.Join(root, clean)
		if err := ensureDirInside(rootResolved, filepath.Dir(fp), insideRoot); err != nil {
			return results, err
		}

		switch op.kind {
		case opAdd:
			if _, err := os.Lstat(fp); err == nil {
				return results, errdefs.Conflictf(
					"apply_patch: file %q already exists", op.path)
			}
			content := strings.Join(op.body, "\n") + "\n"
			if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: op.path, Action: "add"})
		case opUpdate:
			resolved, err := filepath.EvalSymlinks(fp)
			if err != nil || !insideRoot(resolved) {
				return results, errdefs.Validationf(
					"apply_patch: file %q does not exist or escapes %s",
					op.path, dir)
			}
			data, err := os.ReadFile(fp)
			if err != nil {
				return results, err
			}
			lines := splitLines(string(data))
			for _, h := range op.hunks {
				next, found := applyHunk(lines, h)
				if !found {
					return results, errdefs.Validationf(
						"apply_patch: hunk in %q did not match (anchor %q)",
						op.path, h.anchor)
				}
				lines = next
			}
			if err := os.WriteFile(fp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: op.path, Action: "update"})
		case opDelete:
			if _, err := os.Lstat(fp); err != nil {
				return results, errdefs.NotFoundf(
					"apply_patch: file %q does not exist", op.path)
			}
			if err := os.Remove(fp); err != nil {
				return results, err
			}
			results = append(results, FileResult{Path: op.path, Action: "delete"})
		}
	}
	return results, nil
}

// ensureDirInside creates the parent directory and verifies its
// resolved path stays inside root, so a symlinked subdirectory cannot
// redirect a write outside the target.
func ensureDirInside(
	rootResolved string,
	parent string,
	insideRoot func(string) bool,
) error {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	if !insideRoot(resolved) {
		return errdefs.Validationf(
			"apply_patch: directory %q escapes the target", parent)
	}
	return nil
}
