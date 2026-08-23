package desktop

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListDir returns one level of directory entries for the workspace
// panel. Hidden entries and build artifacts (node_modules, .git,
// dist) are skipped; the frontend lazy-loads children on expand.
func (a *App) ListDir(dir string) ([]FileNode, error) {
	if dir == "" {
		dir = a.workDir
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

// OpenPath opens a file or directory with the system default
// application.
func (a *App) OpenPath(path string) error {
	cmd := openCommand(path)
	if err := cmd.Start(); err != nil {
		return err
	}
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
