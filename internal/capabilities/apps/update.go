// Updating one installed application in place, and rolling it back to the
// version the update replaced.
//
// Neither operation touches the state root: sessions, the private
// workspace, approvals and usage live under <dataDir>/apps/<id>, and an
// update that overwrote them would be a reinstall rather than an update.
// Nor is the live content root ever written to in place — a package is
// staged inside the registry and validated *there*, on the bytes that
// would actually land, and only then swapped in, so a refused update
// leaves the installed version serving and the source directory exactly
// as the user's disk had it.

package apps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/utils/semver"
	"github.com/GizClaw/opencraft/internal/foundation/utils/zipx"
)

// backupDir is where the version an update replaced is kept, so a
// rollback can put it back. One snapshot per application: the point of a
// rollback is the version that was running before the update in front of
// the user, not a history.
func (s *Store) backupDir(id string) string {
	return filepath.Join(s.root, ".backups", id)
}

// canRollback reports whether an update left a version behind. The
// snapshot has to carry a manifest to count: a directory without one is
// the leftover of an interrupted write, not a version anyone can run.
func (s *Store) canRollback(id string) bool {
	info, err := os.Stat(filepath.Join(s.backupDir(id), ManifestFile))
	return err == nil && info.Mode().IsRegular()
}

// Update replaces an installed application's content with a newer package
// directory. Everything outside the content root is preserved: the enable
// state, the state root, and the previous rollback snapshot — a failed
// update leaves both the installed version and the snapshot it would have
// replaced untouched.
func (s *Store) Update(ctx context.Context, id, src string) (Summary, error) {
	content, builtin, err := s.contentDir(id)
	if err != nil {
		return Summary{}, err
	}
	if builtin {
		return Summary{}, fmt.Errorf(
			"apps: %q is built in and cannot be updated; install the package under another id",
			id)
	}
	if err := s.checkSource(src); err != nil {
		return Summary{}, err
	}
	m, err := s.readManifest(src, "")
	if err != nil {
		return Summary{}, err
	}
	if m.ID != id {
		return Summary{}, fmt.Errorf(
			"apps: package declares id %q but updates %q", m.ID, id)
	}
	if err := s.checkHostVersion(m); err != nil {
		return Summary{}, err
	}
	// A package is an update only when it is newer than what is
	// installed. The one exception is an install whose manifest cannot be
	// read any more: there is no version to compare against and no
	// document to keep, so what the update replaces is broken bytes —
	// which is exactly the repair the card's error is asking for.
	if cur, err := s.readManifest(content, id); err == nil {
		cmp, err := semver.Compare(m.Version, cur.Version)
		if err != nil {
			return Summary{}, fmt.Errorf("apps: %s: %w", ManifestFile, err)
		}
		if cmp <= 0 {
			return Summary{}, fmt.Errorf(
				"apps: update %q version %s is not newer than the installed %s",
				id, m.Version, cur.Version)
		}
	}
	// The new files land where the installed ones are, so the platform's
	// verdict is about the same paths an install would check.
	if err := s.checkLandingPaths(src, content); err != nil {
		return Summary{}, err
	}
	staging, err := os.MkdirTemp(s.root, ".update-")
	if err != nil {
		return Summary{}, fmt.Errorf("apps: create update staging dir: %w", err)
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"apps: remove update staging failed", os.RemoveAll(staging))
	}()
	if err := copyTree(src, staging); err != nil {
		return Summary{}, fmt.Errorf("apps: stage %q: %w", id, err)
	}
	if err := Validate(ctx, s.appFromManifest(m, staging, true)); err != nil {
		return Summary{}, err
	}
	backup := s.backupDir(id)
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return Summary{}, fmt.Errorf("apps: create backup dir: %w", err)
	}
	// The version being replaced becomes the pending snapshot first: a
	// failed swap then still has the installed content on disk, and the
	// previous snapshot is only replaced once the new version is live.
	pending := backup + ".pending"
	telemetry.WarnErr(context.Background(),
		"apps: clear pending rollback snapshot failed", os.RemoveAll(pending))
	if err := os.Rename(content, pending); err != nil {
		return Summary{}, fmt.Errorf("apps: snapshot %q: %w", id, err)
	}
	if err := os.Rename(staging, content); err != nil {
		restoreErr := os.Rename(pending, content)
		telemetry.WarnErr(context.Background(),
			"apps: restore %q after update swap failure", restoreErr)
		return Summary{}, fmt.Errorf(
			"apps: update %q: %w (restore: %v)", id, err, restoreErr)
	}
	// The new version is live from here on, whatever the snapshot
	// bookkeeping below does: the registry's answer to "what runs" is the
	// content root.
	old := backup + ".old"
	telemetry.WarnErr(context.Background(),
		"apps: clear previous rollback snapshot failed", os.RemoveAll(old))
	if _, err := os.Stat(backup); err == nil {
		if err := os.Rename(backup, old); err != nil {
			return Summary{}, fmt.Errorf(
				"apps: replace rollback snapshot %q: %w", id, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Summary{}, fmt.Errorf("apps: stat rollback snapshot %q: %w", id, err)
	}
	if err := os.Rename(pending, backup); err != nil {
		restoreErr := os.Rename(old, backup)
		telemetry.WarnErr(context.Background(),
			"apps: restore previous rollback snapshot failed", restoreErr)
		return Summary{}, fmt.Errorf(
			"apps: commit rollback snapshot %q: %w (restore previous: %v)",
			id, err, restoreErr)
	}
	telemetry.WarnErr(context.Background(),
		"apps: remove previous rollback snapshot failed", os.RemoveAll(old))
	return s.summaryOf(m, id), nil
}

// UpdateZip updates an application from a zip package (a release
// artifact, an exported directory), with the same entry-point rules an
// install has.
func (s *Store) UpdateZip(ctx context.Context, id, zipPath string) (Summary, error) {
	dir, cleanup, err := zipx.Extract(zipPath, ManifestFile)
	if err != nil {
		return Summary{}, err
	}
	defer cleanup()
	return s.Update(ctx, id, dir)
}

// Rollback restores the version the last update replaced and consumes the
// snapshot: there is one step back, and taking it puts the application
// where it was before that update. Like an update, it leaves the state
// root and the enable state alone.
func (s *Store) Rollback(ctx context.Context, id string) (Summary, error) {
	content, builtin, err := s.contentDir(id)
	if err != nil {
		return Summary{}, err
	}
	if builtin {
		return Summary{}, fmt.Errorf(
			"apps: %q is built in and cannot be rolled back", id)
	}
	if !s.canRollback(id) {
		return Summary{}, fmt.Errorf("apps: %q has no update to roll back to", id)
	}
	backup := s.backupDir(id)
	m, err := s.readManifest(backup, id)
	if err != nil {
		return Summary{}, err
	}
	if err := s.checkHostVersion(m); err != nil {
		return Summary{}, err
	}
	// The snapshot is validated where it lies, before anything moves: a
	// version that cannot run any more (a newer host, a layer the
	// contract has since refused) is refused here rather than restored.
	if err := Validate(ctx, s.appFromManifest(m, backup, true)); err != nil {
		return Summary{}, err
	}
	discard := filepath.Join(filepath.Dir(backup), id+".discard")
	telemetry.WarnErr(context.Background(),
		"apps: clear discard snapshot failed", os.RemoveAll(discard))
	if err := os.Rename(content, discard); err != nil {
		return Summary{}, fmt.Errorf("apps: move current %q aside: %w", id, err)
	}
	if err := os.Rename(backup, content); err != nil {
		restoreErr := os.Rename(discard, content)
		telemetry.WarnErr(context.Background(),
			"apps: restore %q after rollback failure", restoreErr)
		return Summary{}, fmt.Errorf(
			"apps: roll back %q: %w (restore current: %v)", id, err, restoreErr)
	}
	// The rolled-back version is live from here on; the discard step
	// below only removes what it just replaced.
	telemetry.WarnErr(context.Background(),
		"apps: remove discard snapshot failed", os.RemoveAll(discard))
	return s.summaryOf(m, id), nil
}

// summaryOf is the card view of an installed application right after a
// write: the manifest's facts plus the two things the registry knows
// about it — whether it is enabled, and whether a rollback is available.
func (s *Store) summaryOf(m *Manifest, id string) Summary {
	enabled := true
	if state, err := s.readState(); err == nil {
		enabled = s.enabled(state, id)
	}
	sum := summaryFromManifest(m, enabled)
	sum.CanRollback = s.canRollback(id)
	return sum
}
