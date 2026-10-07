// Turning a validated package into an installed application: the copy,
// the manifest rewrite the wizard's form asks for, and the zip entry
// point.

package apps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/utils/zipx"
)

// InstallOptions are the manifest fields the import wizard lets a user
// edit before an install copies anything.
//
// The zero value installs the package exactly as it is written: an empty
// ID or Name is not a value a manifest could hold — ParseManifest
// refuses both — so "" is unambiguous as "the user did not edit this".
// An icon is the exception, because an application may legitimately have
// none: Icon is a pointer so a form can tell "leave it alone" (nil) from
// "remove it" (a pointer to "").
type InstallOptions struct {
	// ID installs the application under a different id. The id names the
	// install directory and the state root, so it is the one field a user
	// usually has to set: a downloaded package is rarely named after the
	// application.
	ID string
	// Name is the display name the cards and the sidebar show.
	Name string
	// Icon replaces the manifest's icon; a pointer to "" removes it.
	Icon *string
}

// InstallOptionsFromSummary is the wizard's starting point: what the
// manifest declares, as the fields the user edits.
func InstallOptionsFromSummary(sum Summary) InstallOptions {
	icon := sum.Icon
	return InstallOptions{ID: sum.ID, Name: sum.Name, Icon: &icon}
}

// Install copies a package directory into the registry and enables it.
// The install is all-or-nothing by construction: the package is staged
// inside the registry and validated *there* — the bytes that would
// actually land — and only then renamed into place. A refused install
// leaves nothing behind, not even its staging directory, and never
// touches the source directory.
func (s *Store) Install(
	ctx context.Context,
	src string,
	opts InstallOptions,
) (Summary, error) {
	if strings.TrimSpace(s.root) == "" {
		return Summary{}, errors.New("apps: content root is not configured")
	}
	if err := s.checkSource(src); err != nil {
		return Summary{}, err
	}
	m, err := s.readManifest(src, "")
	if err != nil {
		return Summary{}, err
	}
	// An edited field is written into the staged copy, never into the
	// source: the user's directory is theirs, and a refused install must
	// leave it exactly as it was.
	rewritten, manifest, err := rewriteManifest(m, opts)
	if err != nil {
		return Summary{}, err
	}
	if err := s.checkHostVersion(manifest); err != nil {
		return Summary{}, err
	}
	dst := filepath.Join(s.root, manifest.ID, "content")
	if _, err := os.Stat(dst); err == nil {
		return Summary{}, fmt.Errorf(
			"apps: %q is already installed; uninstall it first", manifest.ID)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Summary{}, fmt.Errorf("apps: check destination: %w", err)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return Summary{}, fmt.Errorf("apps: create content root: %w", err)
	}
	staging, err := os.MkdirTemp(s.root, ".install-")
	if err != nil {
		return Summary{}, fmt.Errorf("apps: create staging dir: %w", err)
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"apps: remove install staging failed", os.RemoveAll(staging))
	}()
	if err := copyTree(src, staging); err != nil {
		return Summary{}, fmt.Errorf("apps: stage %q: %w", manifest.ID, err)
	}
	if rewritten != nil {
		if err := os.WriteFile(
			filepath.Join(staging, ManifestFile), rewritten, 0o600,
		); err != nil {
			return Summary{}, fmt.Errorf("apps: write %s: %w", ManifestFile, err)
		}
	}
	if err := Validate(ctx, s.appFromManifest(manifest, staging, true)); err != nil {
		return Summary{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return Summary{}, fmt.Errorf("apps: create application dir: %w", err)
	}
	if err := os.Rename(staging, dst); err != nil {
		return Summary{}, fmt.Errorf("apps: install %q: %w", manifest.ID, err)
	}
	// The install is live from here on, whatever the state write does:
	// the registry's answer to "is it installed" is the content root.
	if err := s.setEnabledState(manifest.ID, true); err != nil {
		return Summary{}, err
	}
	return summaryFromManifest(manifest, true), nil
}

// InstallZip installs an application from a zip package (a release
// artifact, an exported directory). The archive may carry the package
// files at its root or under a single top-level directory; app.yaml is
// located and that folder is installed through the normal path.
func (s *Store) InstallZip(
	ctx context.Context,
	zipPath string,
	opts InstallOptions,
) (Summary, error) {
	dir, cleanup, err := zipx.Extract(zipPath, ManifestFile)
	if err != nil {
		return Summary{}, err
	}
	defer cleanup()
	return s.Install(ctx, dir, opts)
}

// rewriteManifest applies the form's edits to a parsed manifest and
// returns the bytes to write plus the manifest they encode, or nil bytes
// when nothing was edited (the source file is then copied verbatim,
// comments and formatting included).
//
// The edited document goes back through ParseManifest: an id the user
// typed is validated exactly like one an author wrote, and the bytes
// written are the ones that were validated.
func rewriteManifest(
	m *Manifest,
	opts InstallOptions,
) ([]byte, *Manifest, error) {
	next := *m
	edited := false
	if id := strings.TrimSpace(opts.ID); id != "" && id != next.ID {
		next.ID = id
		edited = true
	}
	if name := strings.TrimSpace(opts.Name); name != "" && name != next.Name {
		next.Name = name
		edited = true
	}
	if opts.Icon != nil {
		if icon := strings.TrimSpace(*opts.Icon); icon != next.Icon {
			next.Icon = icon
			edited = true
		}
	}
	if !edited {
		return nil, m, nil
	}
	raw, err := yaml.Marshal(next)
	if err != nil {
		return nil, nil, fmt.Errorf("apps: encode %s: %w", ManifestFile, err)
	}
	parsed, err := ParseManifest(raw)
	if err != nil {
		return nil, nil, err
	}
	return raw, parsed, nil
}
