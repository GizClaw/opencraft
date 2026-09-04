package plugins

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

const (
	maxPluginZipFile  = 64 << 20  // 64 MiB per entry
	maxPluginZipTotal = 256 << 20 // 256 MiB per archive
)

// InstallZip installs a plugin from a zip package (e.g. a GitHub
// release artifact). The archive may carry the plugin files at its
// root or under a single top-level directory; plugin.json is located
// and that folder is installed through the normal Install path
// (manifest validation, exec bit, ad-hoc signing).
func (s *Store) InstallZip(zipPath string) (PluginSummary, error) {
	dir, cleanup, err := extractPluginZip(zipPath)
	if err != nil {
		return PluginSummary{}, err
	}
	defer cleanup()
	return s.Install(dir)
}

// extractPluginZip extracts a plugin package into a temporary
// directory and returns it plus a cleanup func. The archive may carry
// the plugin files at its root or under a single top-level directory;
// plugin.json is located and that folder is returned.
func extractPluginZip(zipPath string) (string, func(), error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", nil, fmt.Errorf("plugins: open zip: %w", err)
	}
	tmp, err := os.MkdirTemp("", "oc-plugin-*")
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"plugins: close zip after temp dir failure", zr.Close())
		return "", nil, fmt.Errorf("plugins: temp dir: %w", err)
	}
	cleanup := func() {
		telemetry.WarnErr(context.Background(),
			"plugins: close zip during cleanup failed", zr.Close())
		telemetry.WarnErr(context.Background(),
			"plugins: remove zip extract temp failed", os.RemoveAll(tmp))
	}

	var total int64
	manifestDir := ""
	for _, f := range zr.File {
		name := filepath.Clean(f.Name)
		if name == ".." ||
			strings.HasPrefix(name, ".."+string(filepath.Separator)) ||
			filepath.IsAbs(name) {
			cleanup()
			return "", nil, fmt.Errorf(
				"plugins: zip entry escapes archive: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > maxPluginZipFile ||
			total+int64(f.UncompressedSize64) > maxPluginZipTotal {
			cleanup()
			return "", nil, fmt.Errorf(
				"plugins: zip entry too large: %q", f.Name)
		}
		total += int64(f.UncompressedSize64)

		target := filepath.Join(tmp, name)
		if !strings.HasPrefix(target, tmp+string(filepath.Separator)) {
			cleanup()
			return "", nil, fmt.Errorf(
				"plugins: zip entry escapes archive: %q", f.Name)
		}
		if filepath.Base(name) == "plugin.json" {
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
				"plugins: close zip entry after open output failure", rc.Close())
			cleanup()
			return "", nil, err
		}
		_, copyErr := io.Copy(out, rc)
		telemetry.WarnErr(context.Background(),
			"plugins: close zip entry after copy failed", rc.Close())
		telemetry.WarnErr(context.Background(),
			"plugins: close extracted output failed", out.Close())
		if copyErr != nil {
			cleanup()
			return "", nil, fmt.Errorf(
				"plugins: extract %q: %w", f.Name, copyErr)
		}
	}
	if manifestDir == "" {
		cleanup()
		return "", nil, errors.New("plugins: zip has no plugin.json")
	}
	if manifestDir == "." {
		manifestDir = ""
	}
	return filepath.Join(tmp, manifestDir), cleanup, nil
}
