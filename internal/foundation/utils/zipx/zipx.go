// Package zipx extracts one package archive (a plugin, an application)
// into a temporary directory.
//
// Both registries install from a directory and accept the same zip
// package shape, so the rules live here once: entries must be relative
// references inside the archive, sizes are bounded per entry and per
// archive, and the package is located by its manifest rather than by the
// archive's layout — the files may sit at the root or under a single
// top-level directory, which is what a GitHub release artifact or a
// zipped checkout looks like.
package zipx

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
)

// Bounds. A package is code and configuration; anything past these
// numbers is a mistake or an attack, and either way the host has to
// refuse it before it writes anything.
const (
	// MaxFile is the size one entry may decompress to.
	MaxFile = 64 << 20 // 64 MiB
	// MaxTotal is the size the whole archive may decompress to.
	MaxTotal = 256 << 20 // 256 MiB
)

// TooLarge is the refusal about an archive's declared sizes: one entry
// past MaxFile, or the whole archive past MaxTotal. The numbers come
// from the archive's own directory and are checked before anything is
// decompressed, which is what makes a zip bomb a refusal rather than an
// out-of-memory kill.
//
// It is a type rather than a formatted string so the sentence can be
// written for the person who packaged the archive and read on its own:
// it names the entry that broke the bound and the bound it broke, and it
// carries no package prefix, because a prefixed one ("zipx: zip entry
// too large: \"big.bin\"") is a log line, not something to show a user.
type TooLarge struct {
	// Entry is the entry that broke the bound.
	Entry string
	// Size is what the archive declares for Entry, or the running total
	// once Entry is counted when Total is set.
	Size int64
	// Limit is the bound that was broken: MaxFile, or MaxTotal when
	// Total is set.
	Limit int64
	// Total reports the refusal is about the archive as a whole rather
	// than about Entry alone.
	Total bool
}

func (e *TooLarge) Error() string {
	if e.Total {
		return fmt.Sprintf(
			"the archive unpacks to %s in total, past the %s the host "+
				"accepts for one package",
			formatSize(e.Size), formatSize(e.Limit))
	}
	return fmt.Sprintf(
		"the archive declares %s for %q, past the %s the host accepts for "+
			"one file",
		formatSize(e.Size), e.Entry, formatSize(e.Limit))
}

// formatSize renders a byte count the way a packager reads it: whole
// mebibytes with one decimal, because the bounds are stated in MiB and
// "67371008 bytes" is not a number anyone compares against a limit.
func formatSize(n int64) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// Extract unpacks one package archive into a temporary directory and
// returns the directory holding the package — the one the manifest named
// by marker lives in — plus a cleanup function that closes the archive
// and removes the extraction root.
//
// A refused archive leaves nothing behind: the extraction root is
// removed before the error is returned.
func Extract(zipPath, marker string) (string, func(), error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		// The user picked something that is not an archive — a renamed
		// tarball, an HTML error page saved as .zip — which is about the
		// pick rather than about a package inside it, so the sentence
		// names the file they chose.
		if errors.Is(err, zip.ErrFormat) {
			return "", nil, fmt.Errorf(
				"zipx: %s is not a zip archive", filepath.Base(zipPath))
		}
		return "", nil, fmt.Errorf("zipx: open zip: %w", err)
	}
	tmp, err := os.MkdirTemp("", "oc-package-*")
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"zipx: close zip after temp dir failure", zr.Close())
		return "", nil, fmt.Errorf("zipx: temp dir: %w", err)
	}
	cleanup := func() {
		telemetry.WarnErr(context.Background(),
			"zipx: close zip during cleanup failed", zr.Close())
		telemetry.WarnErr(context.Background(),
			"zipx: remove zip extract temp failed", os.RemoveAll(tmp))
	}

	var total int64
	manifestDir := ""
	for _, f := range zr.File {
		name := filepath.Clean(f.Name)
		if !pathsafe.RelRef(name) {
			cleanup()
			return "", nil, fmt.Errorf(
				"zipx: zip entry escapes archive: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		entry := int64(f.UncompressedSize64)
		switch {
		case entry > MaxFile:
			cleanup()
			return "", nil, &TooLarge{Entry: f.Name, Size: entry, Limit: MaxFile}
		case total+entry > MaxTotal:
			cleanup()
			return "", nil, &TooLarge{
				Entry: f.Name,
				Size:  total + entry,
				Limit: MaxTotal,
				Total: true,
			}
		}
		total += entry

		// RelRef on the cleaned name is the zip-slip gate: absolute
		// names and ".." components are rejected above, so this join
		// can no longer leave the extraction root.
		target := filepath.Join(tmp, name)
		if filepath.Base(name) == marker {
			manifestDir = filepath.Dir(name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			cleanup()
			return "", nil, err
		}
		rc, err := f.Open()
		if err != nil {
			cleanup()
			return "", nil, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			telemetry.WarnErr(context.Background(),
				"zipx: close zip entry after open output failure", rc.Close())
			cleanup()
			return "", nil, err
		}
		_, copyErr := io.Copy(out, rc)
		telemetry.WarnErr(context.Background(),
			"zipx: close zip entry after copy failed", rc.Close())
		telemetry.WarnErr(context.Background(),
			"zipx: close extracted output failed", out.Close())
		if copyErr != nil {
			cleanup()
			return "", nil, fmt.Errorf("zipx: extract %q: %w", f.Name, copyErr)
		}
	}
	if manifestDir == "" {
		cleanup()
		return "", nil, fmt.Errorf("zipx: zip has no %s", marker)
	}
	if manifestDir == "." {
		manifestDir = ""
	}
	return filepath.Join(tmp, manifestDir), cleanup, nil
}
