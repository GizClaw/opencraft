package skills

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// Curator owns the skill lifecycle: which skills look retired-in-
// waiting (idle and rarely used), how a retirement is snapshotted, and
// how it is undone. It never deletes: retiring writes a tar.gz
// snapshot under the archive directory, records it in user.db and
// flags the skill as retired, so the registry stops offering it while
// every byte stays recoverable.
//
// Builtins never participate: they ship with the app and are not the
// user's to retire.
type Curator struct {
	svc        *Service
	store      skillusage.Lifecycle
	settings   config.SkillLifecycleConfig
	archiveDir string
	now        func() time.Time
}

// Candidate is one skill that looks ready to retire.
type Candidate struct {
	Name     string    `json:"name"`
	Scope    string    `json:"scope"`
	Path     string    `json:"path"`
	Uses     int       `json:"uses"`
	LastUsed time.Time `json:"last_used,omitzero"`
	IdleDays int       `json:"idle_days"`
	// Reason explains the verdict in one phrase for the UI: "never
	// used" or "n uses, idle for d days".
	Reason string `json:"reason"`
}

// NewCurator builds a curator over the registry. store may be nil (no
// user database): candidates then come from file times alone and
// retiring reports NotAvailable.
func NewCurator(
	svc *Service,
	store skillusage.Lifecycle,
	settings config.SkillLifecycleConfig,
	archiveDir string,
) *Curator {
	return &Curator{
		svc:        svc,
		store:      store,
		settings:   settings,
		archiveDir: archiveDir,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Enabled reports whether the curator can do anything in this runtime.
func (c *Curator) Enabled() bool {
	return c != nil && c.svc != nil && c.store != nil && !c.store.Empty()
}

// Candidates lists the retirement candidates: user-scope skills that
// are neither pinned nor retired, were used fewer than MinUses times
// inside the usage window, and have been idle longer than
// StaleAfterDays (a skill that was never used counts from its own file
// time, so a freshly installed skill is not a candidate on day one).
func (c *Curator) Candidates(ctx context.Context) ([]Candidate, error) {
	if !c.Enabled() {
		return nil, nil
	}
	stats, err := c.store.Stats(ctx, c.usageWindowStart())
	if err != nil {
		return nil, err
	}
	states, err := c.store.States(ctx)
	if err != nil {
		return nil, err
	}
	now := c.now()
	byName := make(map[string]skillusage.Stat, len(stats))
	for _, stat := range stats {
		byName[stat.Name] = stat
	}
	var out []Candidate
	for _, sk := range c.svc.List() {
		if sk.Scope == "builtin" {
			continue
		}
		if state, ok := states[sk.Name]; ok && (state.Pinned || state.Retired) {
			continue
		}
		stat := byName[sk.Name]
		idleSince := stat.LastUsed
		if idleSince.IsZero() {
			// Never used: measure idleness from the skill file itself
			// so a new skill gets its grace period.
			idleSince = skillModTime(sk.Path, now)
		}
		idleDays := int(now.Sub(idleSince).Hours() / 24)
		if idleDays < c.settings.StaleAfterDays {
			continue
		}
		if stat.Uses >= c.settings.MinUses {
			continue
		}
		reason := fmt.Sprintf("never used, untouched for %d days", idleDays)
		if stat.Uses > 0 {
			reason = fmt.Sprintf("%d use(s), idle for %d days",
				stat.Uses, idleDays)
		}
		out = append(out, Candidate{
			Name:     sk.Name,
			Scope:    sk.Scope,
			Path:     sk.Path,
			Uses:     stat.Uses,
			LastUsed: stat.LastUsed,
			IdleDays: idleDays,
			Reason:   reason,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IdleDays != out[j].IdleDays {
			return out[i].IdleDays > out[j].IdleDays
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Retire snapshots one skill directory and marks the skill retired. The
// directory stays where it is (nothing is deleted and the registry only
// stops injecting it), and the returned record is what Restore undoes.
func (c *Curator) Retire(
	ctx context.Context, name string,
) (skillusage.Archive, error) {
	if !c.Enabled() {
		return skillusage.Archive{}, errdefs.NotAvailablef(
			"skills: no user database in this runtime")
	}
	sk, ok := c.svc.ByName(name)
	if !ok {
		return skillusage.Archive{}, errdefs.NotFoundf(
			"skills: skill %q not found", name)
	}
	if sk.Scope == "builtin" {
		return skillusage.Archive{}, errdefs.Validationf(
			"skills: builtin skill %q is not managed by the curator", name)
	}
	// Retiring is not idempotent: a second pass snapshots again and
	// would leave the record the first one is restored from pointing at
	// a snapshot the second one overwrote.
	if states, err := c.store.States(ctx); err != nil {
		return skillusage.Archive{}, err
	} else if state, ok := states[name]; ok && state.Retired {
		return skillusage.Archive{}, errdefs.Validationf(
			"skills: %q is already retired", name)
	}
	if strings.TrimSpace(c.archiveDir) == "" {
		return skillusage.Archive{}, errdefs.Validationf(
			"skills: no archive directory is configured")
	}
	dir := filepath.Dir(sk.Path)
	archivePath, err := c.snapshot(name, dir)
	if err != nil {
		return skillusage.Archive{}, err
	}
	record := skillusage.Archive{
		ID:          archiveID(name, c.now()),
		Name:        name,
		Scope:       sk.Scope,
		SkillPath:   dir,
		ArchivePath: archivePath,
		CreatedAt:   c.now(),
	}
	if err := c.store.RecordArchive(ctx, record); err != nil {
		// The snapshot exists but nothing references it: drop it rather
		// than leave an orphan the page cannot see.
		telemetry.WarnErr(ctx, "skills: remove orphaned archive snapshot failed",
			os.Remove(archivePath))
		return skillusage.Archive{}, err
	}
	if _, err := c.store.SetRetired(ctx, name, sk.Scope, true); err != nil {
		return skillusage.Archive{}, err
	}
	c.svc.Reload()
	telemetry.Info(ctx, "skills: retired",
		otellog.String("skill", name),
		otellog.String("archive", archivePath))
	return record, nil
}

// Restore undoes a retirement: the snapshot is extracted again when the
// skill directory is gone, the retired flag is cleared and the registry
// rediscovers. Restoring an already restored record is a no-op.
func (c *Curator) Restore(ctx context.Context, id string) (skillusage.Archive, error) {
	if !c.Enabled() {
		return skillusage.Archive{}, errdefs.NotAvailablef(
			"skills: no user database in this runtime")
	}
	record, err := c.store.GetArchive(ctx, id)
	if err != nil {
		return skillusage.Archive{}, err
	}
	if _, statErr := os.Stat(record.SkillPath); statErr != nil {
		if err := extractArchive(record.ArchivePath, record.SkillPath); err != nil {
			return skillusage.Archive{}, err
		}
	}
	if _, err := c.store.SetRetired(ctx, record.Name, record.Scope, false); err != nil {
		return skillusage.Archive{}, err
	}
	if !record.RestoredAt.IsZero() {
		// Already restored once (a second retire/resume cycle keeps the
		// same record): the flag is what matters now.
		c.svc.Reload()
		return record, nil
	}
	if err := c.store.MarkRestored(ctx, id); err != nil {
		return skillusage.Archive{}, err
	}
	c.svc.Reload()
	telemetry.Info(ctx, "skills: restored",
		otellog.String("skill", record.Name))
	return record, nil
}

// Archives returns every snapshot record, newest first.
func (c *Curator) Archives(ctx context.Context) ([]skillusage.Archive, error) {
	if !c.Enabled() {
		return nil, nil
	}
	return c.store.ListArchives(ctx)
}

// Prune drops usage events older than the configured window and returns
// how many rows went away. Aggregates the page shows all live inside
// the window, so pruning only bounds the table.
func (c *Curator) Prune(ctx context.Context) (int64, error) {
	if !c.Enabled() {
		return 0, nil
	}
	before := c.usageWindowStart()
	if before.IsZero() {
		return 0, nil
	}
	return c.store.Prune(ctx, before)
}

// usageWindowStart is the oldest usage event the statistics consider: a
// configured window bounds both the candidate verdict and the prune, so
// a skill last used a year ago reads as "never used inside the window"
// rather than as used. A zero window means all time.
func (c *Curator) usageWindowStart() time.Time {
	if c.settings.UsageWindowDays <= 0 {
		return time.Time{}
	}
	return c.now().AddDate(0, 0, -c.settings.UsageWindowDays)
}

// snapshot writes one tar.gz of dir under the archive directory and
// returns its path. The archive stores paths relative to the skill
// directory, so extracting it restores the directory as it was.
func (c *Curator) snapshot(name, dir string) (string, error) {
	if err := os.MkdirAll(c.archiveDir, 0o700); err != nil {
		return "", fmt.Errorf("skills: create archive dir: %w", err)
	}
	path := filepath.Join(c.archiveDir, archiveID(name, c.now())+".tar.gz")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("skills: create archive %s: %w", path, err)
	}
	if err := writeTarGz(file, dir); err != nil {
		telemetry.WarnErr(context.Background(),
			"skills: remove partial archive failed", os.Remove(path))
		telemetry.WarnErr(context.Background(),
			"skills: close failed archive failed", file.Close())
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("skills: close archive %s: %w", path, err)
	}
	return path, nil
}

// writeTarGz streams a directory into w. Symlinks are skipped: a skill
// pointing outside its own directory is not something the curator
// should copy into a snapshot.
func writeTarGz(w io.Writer, dir string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(tw, src); err != nil {
			telemetry.WarnErr(context.Background(),
				"skills: close archive source failed", src.Close())
			return err
		}
		return src.Close()
	})
	if err != nil {
		return fmt.Errorf("skills: archive %s: %w", dir, err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("skills: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("skills: close gzip: %w", err)
	}
	return nil
}

// extractArchive restores one snapshot into dir. Entry names are
// validated against traversal before anything is written.
func extractArchive(archivePath, dir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("skills: open archive %s: %w", archivePath, err)
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"skills: close archive failed", file.Close())
	}()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("skills: read archive %s: %w", archivePath, err)
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"skills: close gzip reader failed", gz.Close())
	}()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("skills: create %s: %w", dir, err)
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("skills: read archive %s: %w", archivePath, err)
		}
		rel := filepath.Clean(filepath.FromSlash(header.Name))
		// A traversal entry is the parent directory itself or something
		// below it: a ".." prefix test would also refuse legitimate
		// names like "..notes.md".
		belowParent := ".." + string(filepath.Separator)
		if rel == "." || filepath.IsAbs(rel) || rel == ".." ||
			strings.HasPrefix(rel, belowParent) {
			return errdefs.Validationf(
				"skills: archive %s holds an unsafe entry %q",
				archivePath, header.Name)
		}
		target := filepath.Join(dir, rel)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("skills: restore %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return fmt.Errorf("skills: restore %s: %w", target, err)
			}
			if err := writeFile(target, tr); err != nil {
				return err
			}
		}
	}
}

func writeFile(path string, r io.Reader) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("skills: create %s: %w", path, err)
	}
	if _, err := io.Copy(out, r); err != nil {
		telemetry.WarnErr(context.Background(),
			"skills: close partial restore failed", out.Close())
		return fmt.Errorf("skills: write %s: %w", path, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("skills: close %s: %w", path, err)
	}
	return nil
}

// archiveID is the snapshot's name: the skill name, the moment it was
// archived, and a random suffix, sanitized for a file name. The suffix
// is what makes two retires inside one wall-clock second land in two
// files: the snapshot path is opened with O_TRUNC, so a shared name
// would destroy the first archive while its record kept pointing at it.
func archiveID(name string, at time.Time) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	return fmt.Sprintf("%s-%s-%s", safe,
		at.UTC().Format("20060102T150405"), randomSuffix())
}

// randomSuffix returns four hex characters from the platform CSPRNG,
// falling back to the clock when the source is unavailable.
func randomSuffix() string {
	var buf [2]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%04x", uint16(time.Now().UnixNano()))
	}
	return hex.EncodeToString(buf[:])
}

// skillModTime is the skill's own timestamp, used as the fallback
// "idle since" for a skill that has never been used. A stat failure
// counts as "just touched" so a vanished file is never a candidate.
func skillModTime(path string, now time.Time) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return now
	}
	return info.ModTime().UTC()
}
