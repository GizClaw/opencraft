// Reading a candidate package before it is installed, and the content
// files of one that is: what the import wizard shows and what the
// application page loads.

package apps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
)

// maxAssetBytes bounds one file read out of a content root. An
// application's frontend bundle is a module and a stylesheet; anything
// past this is not one, and the page should not hold it in memory.
const maxAssetBytes = 32 << 20 // 32 MiB

// Inspection is what one candidate package looks like before anything is
// copied: the card it would render as, and every reason the preflight
// refuses it.
//
// Refusals are data, not an error: a package that cannot be installed is
// exactly what the wizard has to explain, one row per problem, and the
// caller that only wants a yes/no reads them for that.
type Inspection struct {
	// Summary is the application the install would produce: the
	// manifest's id, name, version, icon and the entry agent.
	// Summary.Enabled is always true — installing an application enables
	// it — so a wizard that asks "was this enabled?" is asking about an
	// installed application, not a candidate.
	Summary Summary
	// Layers are the deployment layers, in the manifest's order.
	Layers []string
	// Refusals is the preflight's verdict over the package as it lies in
	// its source directory. Empty means it can be installed as it is.
	Refusals []Refusal
}

// Inspect reads one candidate package — a directory holding app.yaml,
// the layers it names and the files they reference — and reports what
// installing it would do. It copies nothing, writes nothing and never
// touches the state root, so it is safe to run on any directory a user
// picked.
//
// A manifest the host cannot even parse is an error: there is nothing to
// report rows about. Everything after it — reserved keys, restricted
// kinds, references leaving the content root, a missing agent — is a
// refusal, because those are the things a user can go and fix.
func (s *Store) Inspect(ctx context.Context, src string) (Inspection, error) {
	if err := s.checkSource(src); err != nil {
		return Inspection{}, err
	}
	m, err := s.readManifest(src, "")
	if err != nil {
		return Inspection{}, err
	}
	if err := s.checkHostVersion(m); err != nil {
		return Inspection{}, err
	}
	insp := Inspection{
		Summary: summaryFromManifest(m, true),
		Layers:  append([]string(nil), m.Layers...),
	}
	err = Validate(ctx, s.appFromManifest(m, src, true))
	var refusals Refusals
	switch {
	case err == nil:
	case errors.As(err, &refusals):
		insp.Refusals = refusals.List
	default:
		return Inspection{}, err
	}
	return insp, nil
}

// ReadAsset returns one file out of an installed application's content
// root: the frontend module the page imports, the stylesheet it injects.
//
// The read is confined to the content root the same way the preflight
// confines the manifest's own references: a relative path that stays
// inside, a regular file, no symbolic link, and no directory that leads
// out of the package. rel is slash-spelled, which is what a manifest
// declares.
func (s *Store) ReadAsset(id, rel string) ([]byte, error) {
	content, _, err := s.contentDir(id)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(rel)
	if trimmed == "" {
		return nil, errors.New("apps: asset path is required")
	}
	if len(trimmed) > maxPathLen || filepath.IsAbs(trimmed) ||
		!pathsafe.RelRef(trimmed) {
		return nil, fmt.Errorf(
			"apps: asset %q must be a relative path inside the content root",
			rel)
	}
	full, err := pathsafe.ResolveUnder(content, trimmed)
	if err != nil {
		return nil, fmt.Errorf("apps: asset %q escapes the content root", rel)
	}
	info, err := os.Lstat(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("apps: %s: asset %q is not there", id, rel)
	case err != nil:
		return nil, fmt.Errorf("apps: asset %q: %w", rel, err)
	case info.IsDir():
		return nil, fmt.Errorf("apps: asset %q is a directory", rel)
	// A link is refused before the confinement check below, for the same
	// reason the install refuses to copy one: the host reads the file
	// through the path it was given, and a link makes "inside the
	// content root" mean two different things.
	case info.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("apps: asset %q is a symbolic link", rel)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("apps: asset %q is not a regular file", rel)
	case info.Size() > maxAssetBytes:
		return nil, fmt.Errorf(
			"apps: asset %q is %d bytes, over the %d byte limit",
			rel, info.Size(), maxAssetBytes)
	}
	// Lstat only resolved the last segment, so a directory on the way in
	// may still be a link out of the package: this is what catches
	// "ui/ → somewhere else" reaching a regular file.
	if !pathsafe.RealWithin(content, full) {
		return nil, fmt.Errorf(
			"apps: asset %q resolves outside the content root", rel)
	}
	return os.ReadFile(full)
}
