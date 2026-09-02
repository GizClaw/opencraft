package toolchain

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ProgressFunc reports runtime extraction progress. done/total count
// uncompressed file bytes so callers can render a percentage.
type ProgressFunc func(family string, done, total int64)

// ensureExtracted unpacks one bundled runtime archive into the user
// cache. It runs only when bundled resolution needs that family and no
// extracted copy exists yet; extraction is atomic (temp dir + rename)
// so concurrent launcher/app processes cannot observe a partial tree.
func (m *Manager) ensureExtracted(family string, progress ProgressFunc) error {
	if m.root == "" {
		return fmt.Errorf("runtimes: no bundled runtime root")
	}
	if m.cacheDir == "" {
		return fmt.Errorf("runtimes: no runtime cache directory")
	}
	if m.manifest == nil {
		return fmt.Errorf("runtimes: no manifest for lazy extraction")
	}
	entry := m.manifest.entry(family)
	if entry == nil || entry.Version == "" {
		return fmt.Errorf("runtimes: %s is not bundled", family)
	}
	platform := platformKey()
	target := filepath.Join(m.cacheDir, family, entry.Version, platform)
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return nil
	}

	archivePath := filepath.Join(m.root, "archives", family, platform)
	data, err := os.ReadFile(archivePath)
	if err != nil {
		return fmt.Errorf(
			"runtimes: read bundled %s archive: %w", family, err)
	}
	want := entry.SHA256[platform]
	if want != "" {
		sum := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
			return fmt.Errorf(
				"runtimes: bundled %s archive sha256 mismatch", family)
		}
	}

	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, "."+family+"-extract-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	total, err := archiveTotal(data)
	if err != nil {
		return fmt.Errorf("runtimes: inspect %s archive: %w", family, err)
	}
	if total > 0 && progress != nil {
		progress(family, 0, total)
	}
	if err := extractArchiveWithProgress(
		data, tmp,
		func(done int64) {
			if progress != nil {
				progress(family, done, total)
			}
		},
	); err != nil {
		return fmt.Errorf("runtimes: extract %s: %w", family, err)
	}
	if total > 0 && progress != nil {
		progress(family, total, total)
	}
	if err := os.Rename(tmp, target); err != nil {
		if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
			return nil // another process won the race
		}
		return fmt.Errorf("runtimes: activate %s cache: %w", family, err)
	}
	return nil
}

func extractArchiveWithProgress(
	data []byte,
	dest string,
	fileProgress func(done int64),
) error {
	switch {
	case len(data) >= 2 && data[0] == 'P' && data[1] == 'K':
		return extractZip(data, dest, fileProgress)
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer func() { _ = gz.Close() }()
		return extractTar(gz, dest, fileProgress)
	default:
		return fmt.Errorf("unsupported runtime archive format")
	}
}

func extractZip(
	data []byte,
	dest string,
	fileProgress func(done int64),
) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	var done int64
	for _, f := range zr.File {
		rel, ok := stripArchiveRoot(f.Name)
		if !ok {
			continue
		}
		out, err := safeJoin(dest, rel)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			continue
		}
		size := int64(f.UncompressedSize64)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		info := f.FileInfo()
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		n, err := writeFile(out, rc, mode)
		_ = rc.Close()
		if err != nil {
			return err
		}
		done += n
		if fileProgress != nil && size > 0 {
			fileProgress(done)
		}
	}
	return nil
}

func extractTar(
	r io.Reader,
	dest string,
	fileProgress func(done int64),
) error {
	tr := tar.NewReader(r)
	var done int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel, ok := stripArchiveRoot(hdr.Name)
		if !ok {
			continue
		}
		out, err := safeJoin(dest, rel)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			n, err := writeFile(out, tr, hdr.FileInfo().Mode().Perm())
			if err != nil {
				return err
			}
			done += n
			if fileProgress != nil && hdr.Size > 0 {
				fileProgress(done)
			}
		case tar.TypeSymlink:
			link := filepath.Clean(filepath.FromSlash(hdr.Linkname))
			if filepath.IsAbs(link) || link == ".." ||
				strings.HasPrefix(link, ".."+string(filepath.Separator)) {
				return fmt.Errorf(
					"archive symlink %q escapes destination", hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, out); err != nil {
				return err
			}
		case tar.TypeLink:
			linkRel, ok := stripArchiveRoot(hdr.Linkname)
			if !ok {
				return fmt.Errorf("hard link %q escapes archive root", hdr.Linkname)
			}
			linkTarget, err := safeJoin(dest, linkRel)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := os.Link(linkTarget, out); err != nil {
				return err
			}
		}
	}
}

func archiveTotal(data []byte) (int64, error) {
	switch {
	case len(data) >= 2 && data[0] == 'P' && data[1] == 'K':
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return 0, err
		}
		var total int64
		for _, f := range zr.File {
			if !f.FileInfo().IsDir() {
				total += int64(f.UncompressedSize64)
			}
		}
		return total, nil
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return 0, err
		}
		defer func() { _ = gz.Close() }()
		tr := tar.NewReader(gz)
		var total int64
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return total, nil
			}
			if err != nil {
				return 0, err
			}
			if hdr.Typeflag == tar.TypeReg {
				total += hdr.Size
			}
		}
	default:
		return 0, fmt.Errorf("unsupported runtime archive format")
	}
}

func stripArchiveRoot(name string) (string, bool) {
	parts := strings.Split(strings.ReplaceAll(name, "\\", "/"), "/")
	if len(parts) <= 1 {
		return "", false
	}
	return strings.Join(parts[1:], "/"), true
}

func safeJoin(dest, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || filepath.IsAbs(clean) ||
		clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path %q escapes destination", rel)
	}
	return filepath.Join(dest, clean), nil
}

func writeFile(path string, r io.Reader, mode os.FileMode) (int64, error) {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(out, r)
	closeErr := out.Close()
	if copyErr != nil {
		return n, copyErr
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(path, mode)
	}
	return n, closeErr
}
