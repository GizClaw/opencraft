// Package update implements the plugin update check/download path
// (manifest-declared update.url). The host fetches a small JSON update
// manifest, validates the version and checksum, then streams the zip
// to disk so the existing Store.UpdateZip pipeline can apply it with
// version constraints and rollback.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	"github.com/GizClaw/opencraft/internal/foundation/utils/netguard"
)

const (
	maxInfoBytes = 1 << 20 // 1 MiB update manifest
	maxZipBytes  = 256 << 20
)

// Policy is the network policy of one update fetch: the shared guards
// the host fetches any manifest-declared URL under (netguard), named
// here so this package's callers do not have to spell out where the
// rules live. AllowPrivate permits loopback/private destinations (used
// by local test servers); the zero value keeps them blocked and
// requires https.
type Policy = netguard.Policy

// CheckWithPolicy fetches and validates the update manifest at sourceURL
// under an explicit network policy.
func CheckWithPolicy(
	ctx context.Context,
	sourceURL string,
	pol Policy,
) (plugins.UpdateInfo, error) {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return plugins.UpdateInfo{}, fmt.Errorf("plugin update: parse url: %w", err)
	}
	if err := validateURL(ctx, u, pol); err != nil {
		return plugins.UpdateInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return plugins.UpdateInfo{}, fmt.Errorf("plugin update: request: %w", err)
	}
	resp, err := client(pol).Do(req)
	if err != nil {
		return plugins.UpdateInfo{}, fmt.Errorf("plugin update: fetch %s: %w", u.Redacted(), err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "plugin update: close manifest response failed",
			resp.Body.Close())
	}()
	if resp.StatusCode != http.StatusOK {
		return plugins.UpdateInfo{}, fmt.Errorf(
			"plugin update: %s returned %s", u.Redacted(), resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInfoBytes+1))
	if err != nil {
		return plugins.UpdateInfo{}, fmt.Errorf("plugin update: read manifest: %w", err)
	}
	if len(body) > maxInfoBytes {
		return plugins.UpdateInfo{}, errors.New("plugin update: manifest exceeds 1 MiB")
	}
	var info plugins.UpdateInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return plugins.UpdateInfo{}, fmt.Errorf("plugin update: decode manifest: %w", err)
	}
	if err := validateInfo(ctx, info, pol); err != nil {
		return plugins.UpdateInfo{}, err
	}
	return info, nil
}

// FetchZip downloads the update package to a temporary file, verifies
// its sha256 checksum, and returns the path plus a cleanup func.
func FetchZip(
	ctx context.Context,
	info plugins.UpdateInfo,
	pol Policy,
) (string, func(), error) {
	if err := validateInfo(ctx, info, pol); err != nil {
		return "", nil, err
	}
	u, err := url.Parse(info.DownloadURL)
	if err != nil {
		return "", nil, fmt.Errorf("plugin update: parse download url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", nil, fmt.Errorf("plugin update: download request: %w", err)
	}
	resp, err := client(pol).Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("plugin update: download %s: %w", u.Redacted(), err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "plugin update: close zip response failed",
			resp.Body.Close())
	}()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf(
			"plugin update: download %s returned %s", u.Redacted(), resp.Status)
	}
	tmp, err := os.CreateTemp("", "oc-plugin-update-*.zip")
	if err != nil {
		return "", nil, fmt.Errorf("plugin update: temp file: %w", err)
	}
	cleanup := func() {
		telemetry.WarnErr(ctx, "plugin update: close zip temp failed", tmp.Close())
		telemetry.WarnErr(ctx, "plugin update: remove zip temp failed",
			os.Remove(tmp.Name()))
	}
	hash := sha256.New()
	written, err := io.Copy(
		io.MultiWriter(tmp, hash),
		io.LimitReader(resp.Body, maxZipBytes+1),
	)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("plugin update: download body: %w", err)
	}
	if written > maxZipBytes {
		cleanup()
		return "", nil, errors.New("plugin update: package exceeds 256 MiB")
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("plugin update: close temp file: %w", err)
	}
	want, err := parseChecksum(info.Checksum)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != want {
		cleanup()
		return "", nil, fmt.Errorf(
			"plugin update: checksum mismatch (want sha256:%s, got sha256:%s)",
			want, got)
	}
	return tmp.Name(), cleanup, nil
}

func validateInfo(ctx context.Context, info plugins.UpdateInfo, pol Policy) error {
	if info.Version == "" {
		return errors.New("plugin update: manifest version is required")
	}
	if err := plugins.ValidateVersion(info.Version); err != nil {
		return fmt.Errorf("plugin update: %w", err)
	}
	if info.DownloadURL == "" {
		return errors.New("plugin update: download_url is required")
	}
	u, err := url.Parse(info.DownloadURL)
	if err != nil {
		return fmt.Errorf("plugin update: parse download_url: %w", err)
	}
	if err := validateURL(ctx, u, pol); err != nil {
		return err
	}
	if _, err := parseChecksum(info.Checksum); err != nil {
		return err
	}
	if len(info.Changelog) > 64<<10 {
		return errors.New("plugin update: changelog exceeds 64 KiB")
	}
	return nil
}

func validateURL(ctx context.Context, u *url.URL, pol Policy) error {
	if err := netguard.CheckURL(ctx, u, pol); err != nil {
		return fmt.Errorf("plugin update: %w", err)
	}
	return nil
}

func parseChecksum(s string) (string, error) {
	if !strings.HasPrefix(s, "sha256:") {
		return "", errors.New("plugin update: checksum must be sha256:<hex>")
	}
	hexPart := strings.TrimPrefix(s, "sha256:")
	if len(hexPart) != sha256.Size*2 {
		return "", errors.New("plugin update: checksum must be sha256:<64 hex chars>")
	}
	_, err := hex.DecodeString(hexPart)
	if err != nil {
		return "", errors.New("plugin update: checksum is not valid hex")
	}
	return strings.ToLower(hexPart), nil
}

// client is the guarded HTTP client of one fetch: the shared rules
// (netguard.Client) under this package's policy.
func client(pol Policy) *http.Client { return netguard.Client(pol) }
