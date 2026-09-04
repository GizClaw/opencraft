package migrations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

// LegacySessionsDir returns the project-local sessions root written by
// v0.1.x releases and by pre-layout callers.
func LegacySessionsDir(workDir string) string {
	return filepath.Join(workDir, ".opencraft", "sessions")
}

// AdoptLegacySessions relocates one workspace's old project-local
// sessions tree into destRoot before the workspace store opens.
// destRoot is the sessions.Store root that owns session.db.
//
// The relocation is a one-time migration: once destRoot owns
// session.db, or when destRoot is already populated, later calls are
// no-ops and Workspace (or the store migration path) imports what is
// already there. Cross-device moves fall back to a copy and remove the
// source only after the copy completes.
func AdoptLegacySessions(
	ctx context.Context, legacyRoot, destRoot string,
) error {
	legacyRoot = filepath.Clean(legacyRoot)
	destRoot = filepath.Clean(destRoot)
	if legacyRoot == destRoot {
		return nil
	}
	if _, err := os.Stat(filepath.Join(destRoot, "session.db")); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("migrations: inspect new session root %s: %w",
			destRoot, err)
	}
	// Only migrate a real project-local tree. Never follow a symlinked
	// .opencraft or sessions entry from an untrusted workspace.
	realParent, err := isRealDirectory(filepath.Dir(legacyRoot))
	if err != nil {
		return fmt.Errorf("migrations: inspect legacy sessions parent: %w", err)
	}
	if !realParent {
		return nil
	}
	realRoot, err := isRealDirectory(legacyRoot)
	if err != nil {
		return err
	}
	if !realRoot {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destRoot), 0o700); err != nil {
		return fmt.Errorf("migrations: create session root parent: %w", err)
	}

	adopted, err := relocateLegacySessions(ctx, legacyRoot, destRoot)
	if err != nil {
		return err
	}
	if adopted {
		telemetry.WarnErr(ctx,
			"migrations: remove empty legacy sessions parent failed",
			removeEmptyDir(filepath.Dir(legacyRoot)))
	}
	return nil
}

// relocateLegacySessions moves legacyRoot into destRoot. It returns
// false (nil error) when destRoot was already populated by another
// adopter or by an earlier interrupted migration, so the caller can
// continue with the normal store/migration path.
func relocateLegacySessions(
	ctx context.Context, legacyRoot, destRoot string,
) (bool, error) {
	entries, err := os.ReadDir(destRoot)
	switch {
	case err == nil && len(entries) > 0:
		return false, nil
	case err == nil:
		if err := os.Remove(destRoot); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			// Another adopter may have moved the tree in after our
			// ReadDir; that is not a failure.
			if _, statErr := os.Stat(legacyRoot); errors.Is(statErr, os.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf(
				"migrations: remove empty session root %s: %w", destRoot, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return false, fmt.Errorf("migrations: read session root %s: %w",
			destRoot, err)
	}

	if err := os.Rename(legacyRoot, destRoot); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		// A concurrent adopter moved the source; the destination now
		// holds the migrated tree.
		if _, statErr := os.Stat(destRoot); statErr == nil {
			return false, nil
		}
		return false, fmt.Errorf(
			"migrations: move legacy sessions %s: %w", legacyRoot, err)
	}

	tmp, err := os.MkdirTemp(filepath.Dir(destRoot), ".adopt-*")
	if err != nil {
		return false, fmt.Errorf("migrations: create adoption temp: %w", err)
	}
	if err := copyLegacyTree(legacyRoot, tmp); err != nil {
		telemetry.WarnErr(ctx,
			"migrations: remove partial legacy session copy failed",
			os.RemoveAll(tmp))
		return false, fmt.Errorf(
			"migrations: copy legacy sessions %s: %w", legacyRoot, err)
	}
	if err := os.Rename(tmp, destRoot); err != nil {
		telemetry.WarnErr(ctx,
			"migrations: remove unused legacy session copy failed",
			os.RemoveAll(tmp))
		if _, statErr := os.Stat(legacyRoot); errors.Is(statErr, os.ErrNotExist) {
			if _, destErr := os.Stat(destRoot); destErr == nil {
				return false, nil
			}
		}
		return false, fmt.Errorf(
			"migrations: finalize legacy sessions copy: %w", err)
	}
	telemetry.WarnErr(ctx, "migrations: remove legacy sessions after copy failed",
		os.RemoveAll(legacyRoot))
	return true, nil
}

// copyLegacyTree copies every regular file under src into dst,
// preserving directory and file permissions. Anything else fails
// closed rather than silently skipping data.
func copyLegacyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(
		path string, entry fs.DirEntry, walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyLegacyFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf(
				"migrations: legacy sessions contains unsupported file %s",
				path)
		}
	})
}

func copyLegacyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"migrations: close legacy session source failed", in.Close())
	}()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		telemetry.WarnErr(context.Background(),
			"migrations: close partial legacy session copy failed", out.Close())
		telemetry.WarnErr(context.Background(),
			"migrations: remove partial legacy session copy failed",
			os.Remove(dst))
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return nil
}

// removeEmptyDir removes dir when it exists and is empty. Missing
// directories are not an error.
func removeEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func isRealDirectory(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir() && info.Mode()&os.ModeSymlink == 0, nil
}
