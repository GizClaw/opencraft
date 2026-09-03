package host

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

var maxManifestEntries = 50_000
var errManifestTooLarge = errors.New("manifest: workspace too large to snapshot")

type fileStat struct {
	Size  int64
	ModNs int64
}

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

var documentExtensions = map[string]bool{
	"md": true, "markdown": true, "txt": true, "rst": true,
	"doc": true, "docx": true,
	"ppt": true, "pptx": true, "key": true,
	"xls": true, "xlsx": true, "csv": true,
	"pdf": true,
}

func isDocumentPath(path string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	return documentExtensions[ext]
}

func diffDocumentArtifacts(
	before, after map[string]fileStat,
) []ocsessions.Artifact {
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

func skipDirEntry(name string) bool {
	switch name {
	case "node_modules", "dist", "build", ".opencraft", ".git", ".github":
		return true
	}
	return strings.HasPrefix(name, ".")
}
