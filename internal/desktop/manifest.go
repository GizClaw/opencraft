package desktop

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

// maxManifestEntries bounds one per-turn workspace walk. Repos larger
// than this skip artifact reconciliation instead of paying a full walk
// twice per turn.
const maxManifestEntries = 50_000

// errManifestTooLarge reports that the workspace exceeded the
// per-turn snapshot budget.
var errManifestTooLarge = errors.New("manifest: workspace too large to snapshot")

// fileStat identifies one workspace file for change detection. ModNs
// is the modification time in Unix nanoseconds; Size plus ModNs catch
// new, modified, and rewritten files without hashing contents.
type fileStat struct {
	Size  int64
	ModNs int64
}

// manifestSnapshot walks the workspace (reusing the file-panel
// exclusions: hidden entries, node_modules, dist, build, .opencraft)
// and returns workspace-relative paths to their stats. It is captured
// before a turn starts and again after it ends; the diff is the
// reconciliation for files the workspace observer cannot see (exec
// writing docs directly to disk). Symlinked directories are not
// followed.
func manifestSnapshot(ctx context.Context, wd string) (map[string]fileStat, error) {
	out := make(map[string]fileStat)
	visited := 0
	err := filepath.WalkDir(wd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		visited++
		if visited > maxManifestEntries {
			return fs.SkipAll
		}
		if path == wd {
			return nil
		}
		if d.IsDir() {
			if skipDirEntry(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(wd, path)
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out[filepath.ToSlash(rel)] = fileStat{
			Size:  info.Size(),
			ModNs: info.ModTime().UnixNano(),
		}
		return nil
	})
	if visited > maxManifestEntries {
		return nil, errManifestTooLarge
	}
	return out, err
}

// documentExtensions is the whitelist for exec-produced reconciliation:
// the text/binary documents the turn strip cares about. Code files and
// build output written by exec are deliberately excluded so the strip
// does not flood with every touched file.
var documentExtensions = map[string]bool{
	"md": true, "markdown": true, "txt": true, "rst": true,
	"doc": true, "docx": true,
	"ppt": true, "pptx": true, "key": true,
	"xls": true, "xlsx": true, "csv": true,
	"pdf": true,
}

// isDocumentPath reports whether path is a document artifact by
// extension.
func isDocumentPath(path string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	return documentExtensions[ext]
}

// diffDocumentArtifacts returns the document files that are new or
// changed between a pre-turn and post-turn snapshot, sorted by path
// for deterministic persistence.
func diffDocumentArtifacts(before, after map[string]fileStat) []ocsessions.Artifact {
	var out []ocsessions.Artifact
	for path, st := range after {
		if !isDocumentPath(path) {
			continue
		}
		prev, ok := before[path]
		if ok && prev.Size == st.Size && prev.ModNs == st.ModNs {
			continue
		}
		out = append(out, ocsessions.Artifact{
			Path:  path,
			Bytes: int(st.Size),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}
