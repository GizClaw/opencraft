package skills

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// maxStageBytes bounds one staged skill copy so a giant skill cannot
// fill the sandbox cache.
const maxStageBytes = 10 << 20 // 10 MiB

// Stage copies a skill directory (SKILL.md, scripts/, references/,
// assets/) into dstDir/<name> and returns the staged root. Symlinks
// are copied as regular files (targets are NOT followed), so staging
// cannot escape dstDir. Builtin skills live only in the binary and
// are never staged (callers skip Scope == "builtin").
func (s *Service) Stage(sk SkillMetadata, dstDir string) (string, error) {
	if sk.Scope == "builtin" {
		return "", fmt.Errorf("skills: builtin %q has no files to stage", sk.Name)
	}
	src := filepath.Dir(sk.Path)
	dst := filepath.Join(dstDir, sk.Name)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	var copied int64
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			// Symlinks and specials: skip rather than follow.
			return nil
		}
		if copied+info.Size() > maxStageBytes {
			return fmt.Errorf("skills: staged skill exceeds %d bytes", maxStageBytes)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = in.Close()
			return err
		}
		n, err := io.Copy(out, in)
		closeErr := out.Close()
		_ = in.Close()
		copied += n
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		_ = os.RemoveAll(dst)
		return "", err
	}
	return dst, nil
}
