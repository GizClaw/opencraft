package desktop

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ListDir returns one level of directory entries for the workspace
// panel. Hidden entries and build artifacts (node_modules, .git,
// dist) are skipped; the frontend lazy-loads children on expand.
func (a *App) ListDir(dir string) ([]FileNode, error) {
	wd := a.snapshotWorkDir()
	if dir == "" {
		dir = wd
	}
	dir, err := resolveInWorkspace(wd, dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]FileNode, 0, len(entries))
	for _, entry := range entries {
		if skipDirEntry(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		node := FileNode{
			Name:  entry.Name(),
			Path:  filepath.Join(dir, entry.Name()),
			IsDir: entry.IsDir(),
			Size:  info.Size(),
		}
		if !node.IsDir && entry.Type()&fs.ModeSymlink != 0 {
			if target, err := os.Stat(node.Path); err == nil {
				node.IsDir = target.IsDir()
			}
		}
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// SearchFileHit is one workspace path matched by the @ mention search.
// Path is workspace-relative with forward slashes, so the model can
// resolve it against the worldstate workspace root.
type SearchFileHit struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

const (
	defaultFileSearchLimit = 50
	maxFileSearchVisits    = 20000
)

// SearchFiles returns workspace-relative paths matching query
// (case-insensitive substring on the relative path). The walk is
// bounded, does not follow symlinked directories, and skips hidden
// entries plus build artifacts; directories are included so the picker
// can show folders too.
func (a *App) SearchFiles(query string, limit int) ([]SearchFileHit, error) {
	wd := a.snapshotWorkDir()
	if strings.TrimSpace(query) == "" {
		return []SearchFileHit{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = defaultFileSearchLimit
	}
	q := strings.ToLower(query)
	hits := make([]SearchFileHit, 0, limit)
	visits := 0
	_ = filepath.WalkDir(wd, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == wd {
			return nil
		}
		visits++
		if visits > maxFileSearchVisits {
			return fs.SkipAll
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() && skipDirEntry(name) {
			return fs.SkipDir
		}
		rel, err := filepath.Rel(wd, path)
		if err != nil {
			return nil
		}
		if strings.Contains(strings.ToLower(filepath.ToSlash(rel)), q) {
			hits = append(hits, SearchFileHit{
				Path:  filepath.ToSlash(rel),
				IsDir: d.IsDir(),
			})
			if len(hits) >= limit {
				return fs.SkipAll
			}
		}
		return nil
	})
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].IsDir != hits[j].IsDir {
			return hits[i].IsDir
		}
		return strings.ToLower(hits[i].Path) < strings.ToLower(hits[j].Path)
	})
	return hits, nil
}

// OpenPath opens a file or directory with the system default
// application.
func (a *App) OpenPath(path string) error {
	wd := a.snapshotWorkDir()
	path, err := resolveInWorkspace(wd, path)
	if err != nil {
		return err
	}
	cmd := openCommand(path)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detach so the launcher process does not linger as a zombie.
	_ = cmd.Process.Release()
	return nil
}

// SaveArtifactAs copies a workspace artifact to a user-selected
// destination via the native save dialog.
func (a *App) SaveArtifactAs(path string) (string, error) {
	src, err := a.workspaceRegularFile(path)
	if err != nil {
		return "", err
	}
	texts := a.desktopTexts()
	dest, err := wailsruntime.SaveFileDialog(
		a.appContext(),
		wailsruntime.SaveDialogOptions{
			Title:                texts.saveArtifactTitle,
			DefaultFilename:      filepath.Base(src),
			DefaultDirectory:     filepath.Dir(src),
			CanCreateDirectories: true,
		},
	)
	if err != nil {
		return "", err
	}
	if dest == "" {
		return "", nil
	}
	if filepath.Clean(dest) == filepath.Clean(src) {
		return dest, nil
	}
	if err := copyRegularFile(src, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// RevealArtifact highlights the artifact in the platform file manager
// (Finder, Explorer, or the Linux file manager).
func (a *App) RevealArtifact(path string) error {
	resolved, err := a.workspaceRegularFile(path)
	if err != nil {
		return err
	}
	return revealInFileManager(resolved)
}

// OpenArtifactWith opens the platform "Open With" flow. The concrete
// behavior is per-platform: macOS asks for an app, Windows opens the
// Open With dialog, and Linux falls back to the default opener when no
// portable chooser is available.
func (a *App) OpenArtifactWith(path string) error {
	resolved, err := a.workspaceRegularFile(path)
	if err != nil {
		return err
	}
	return openArtifactWith(resolved, a.desktopTexts().openWithPrompt)
}

func (a *App) workspaceRegularFile(path string) (string, error) {
	wd := a.snapshotWorkDir()
	resolved, err := resolveInWorkspace(wd, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", resolved)
	}
	return resolved, nil
}

func copyRegularFile(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(
		dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// OpenExternal opens a URL in the system default browser. Only
// http(s) URLs are accepted; anything else is rejected so a binding
// argument can never be abused as a local file scheme.
func (a *App) OpenExternal(url string) error {
	if !strings.HasPrefix(url, "https://") &&
		!strings.HasPrefix(url, "http://") {
		return fmt.Errorf("open external: only http(s) URLs are allowed")
	}
	wailsruntime.BrowserOpenURL(a.appContext(), url)
	return nil
}

func skipDirEntry(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "dist", "build", ".opencraft":
		return true
	}
	return false
}
