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
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
)

const (
	maxInfoBytes   = 1 << 20 // 1 MiB update manifest
	maxZipBytes    = 256 << 20
	maxRedirects   = 5
	requestTimeout = 30 * time.Second
	dialTimeout    = 10 * time.Second
)

// Policy controls network restrictions for tests/development.
// AllowPrivate permits loopback/private destinations (used by local
// test servers); the zero value keeps them blocked and requires https.
type Policy struct {
	AllowPrivate bool
}

// Check fetches and validates the update manifest at sourceURL.
func Check(ctx context.Context, sourceURL string) (plugins.UpdateInfo, error) {
	return CheckWithPolicy(ctx, sourceURL, Policy{})
}

// CheckWithPolicy is Check with an explicit network policy.
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf(
			"plugin update: download %s returned %s", u.Redacted(), resp.Status)
	}
	tmp, err := os.CreateTemp("", "oc-plugin-update-*.zip")
	if err != nil {
		return "", nil, fmt.Errorf("plugin update: temp file: %w", err)
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
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
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("plugin update: unsupported scheme %q", u.Scheme)
	}
	if !pol.AllowPrivate && u.Scheme != "https" {
		return errors.New("plugin update: http is only allowed for private/test hosts")
	}
	if u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("plugin update: url must be absolute, without credentials or fragment")
	}
	if err := checkHost(ctx, u.Hostname(), pol); err != nil {
		return err
	}
	return nil
}

func checkHost(ctx context.Context, host string, pol Policy) error {
	if ip := net.ParseIP(host); ip != nil {
		if !pol.AllowPrivate && blockedIP(ip) {
			return fmt.Errorf("plugin update: private address %q is blocked", host)
		}
		return nil
	}
	if pol.AllowPrivate {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(dctx, "ip", host)
	if err != nil {
		return fmt.Errorf("plugin update: resolve %q: %w", host, err)
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return fmt.Errorf(
				"plugin update: host %q resolves to private address %s (blocked)",
				host, ip)
		}
	}
	return nil
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

func parseChecksum(s string) (string, error) {
	if !strings.HasPrefix(s, "sha256:") {
		return "", errors.New("plugin update: checksum must be sha256:<hex>")
	}
	hexPart := strings.TrimPrefix(s, "sha256:")
	if len(hexPart) != sha256.Size*2 {
		return "", errors.New("plugin update: checksum must be sha256:<64 hex chars>")
	}
	raw, err := hex.DecodeString(hexPart)
	if err != nil {
		return "", errors.New("plugin update: checksum is not valid hex")
	}
	_ = raw
	return strings.ToLower(hexPart), nil
}

func client(pol Policy) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(
			ctx context.Context,
			network, addr string,
		) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if err := checkHost(ctx, host, pol); err != nil {
				return nil, err
			}
			dialer := &net.Dialer{Timeout: dialTimeout}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("plugin update: too many redirects")
			}
			return validateURL(req.Context(), req.URL, pol)
		},
	}
}
