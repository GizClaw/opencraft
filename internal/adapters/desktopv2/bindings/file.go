package bindings

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	patchutil "github.com/GizClaw/opencraft/internal/foundation/utils/patch"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// File exposes workspace file browsing operations.
type File struct {
	core *core.Core
}

// NewFileBinding wires the file binding.
func NewFileBinding(c *core.Core) *File {
	return &File{core: c}
}

// FileNode is one entry of the workspace file tree.
type FileNode struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

// List returns one directory level, sorted dirs-first.
func (b *File) List(dir string) ([]FileNode, error) {
	root := b.core.ActiveWorkDir()
	if root == "" {
		return nil, errors.New("file: no workspace selected")
	}
	full := filepath.Join(root, filepath.FromSlash(dir))
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	out := make([]FileNode, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, FileNode{
			Name:  e.Name(),
			Path:  filepath.Join(full, e.Name()),
			IsDir: e.IsDir(),
			Size:  info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// SearchFileHit is a workspace-relative path match.
type SearchFileHit struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// Search returns workspace-relative paths containing query.
func (b *File) Search(query string, limit int) ([]SearchFileHit, error) {
	root := b.core.ActiveWorkDir()
	if root == "" {
		return nil, errors.New("file: no workspace selected")
	}
	if strings.TrimSpace(query) == "" {
		return []SearchFileHit{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := strings.ToLower(query)
	var hits []SearchFileHit
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if strings.Contains(strings.ToLower(filepath.ToSlash(rel)), q) {
			hits = append(hits, SearchFileHit{
				Path:  filepath.ToSlash(rel),
				IsDir: d.IsDir(),
			})
			if len(hits) >= limit {
				return filepath.SkipAll
			}
		}
		return nil
	})
	return hits, nil
}

func (b *File) resolve(path string) (string, error) {
	root := b.core.ActiveWorkDir()
	if root == "" {
		return "", errors.New("file: no workspace selected")
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the workspace", path)
	}
	return abs, nil
}

// ReadText returns a text file from the workspace.
func (b *File) ReadText(path string) (string, error) {
	full, err := b.resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// OpenExternal opens an http(s) URL in the default browser.
func (b *File) OpenExternal(rawURL string) error {
	if !strings.HasPrefix(rawURL, "https://") &&
		!strings.HasPrefix(rawURL, "http://") {
		return fmt.Errorf("open external: only http(s) URLs are allowed")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", rawURL).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
	default:
		return exec.Command("xdg-open", rawURL).Start()
	}
}

// OpenPath opens a file or directory with the system default app.
func (b *File) OpenPath(path string) error {
	full, err := b.resolve(path)
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", full)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", full)
	default:
		cmd = exec.Command("xdg-open", full)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

// Reveal highlights a path in the platform file manager.
func (b *File) Reveal(path string) error {
	full, err := b.resolve(path)
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", full)
	case "windows":
		cmd = exec.Command("explorer", "/select,", full)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(full))
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

// Diff returns the git diff for one workspace path.
func (b *File) Diff(path string) (string, error) {
	ctx := b.core.Shell.Context()
	full, err := b.resolve(path)
	if err != nil {
		return "", err
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		cmdCtx, "git", "-C", b.core.ActiveWorkDir(),
		"diff", "--no-color", "--", full,
	)
	out, err := cmd.Output()
	if err != nil {
		if cmdCtx.Err() != nil {
			return "", errors.New("git diff timed out")
		}
		return "", err
	}
	return string(out), nil
}

// Attachment preview metadata.
type Attachment struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
	DataURL   string `json:"data_url,omitempty"`
}

// ReadAttachment returns preview metadata; images include a data URL.
func (b *File) ReadAttachment(path string) (Attachment, error) {
	full, err := b.resolve(path)
	if err != nil {
		return Attachment{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return Attachment{}, err
	}
	if !info.Mode().IsRegular() {
		return Attachment{}, fmt.Errorf("%s is not a regular file", full)
	}
	if info.Size() > 10<<20 {
		return Attachment{}, fmt.Errorf("attachment too large to preview")
	}
	mediaType := mime.TypeByExtension(filepath.Ext(full))
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	dto := Attachment{
		Name:      filepath.Base(full),
		Path:      full,
		Size:      info.Size(),
		MediaType: mediaType,
	}
	if strings.HasPrefix(mediaType, "image/") {
		data, err := os.ReadFile(full)
		if err != nil {
			return Attachment{}, err
		}
		dto.DataURL = "data:" + mediaType + ";base64," +
			base64.StdEncoding.EncodeToString(data)
	}
	return dto, nil
}

// PatchFile is one changed file in a rendered codex patch.
type PatchFile struct {
	Path    string      `json:"path"`
	Action  string      `json:"action"`
	Added   int         `json:"added"`
	Removed int         `json:"removed"`
	Lines   []PatchLine `json:"lines"`
}

// PatchLine is one rendered diff line.
type PatchLine struct {
	Kind   string `json:"kind"`
	OldNum int    `json:"old_num"`
	NewNum int    `json:"new_num"`
	Text   string `json:"text"`
}

// RenderPatch renders a codex patch against workspace files.
func (b *File) RenderPatch(patch string) ([]PatchFile, error) {
	root := b.core.ActiveWorkDir()
	if root == "" {
		return nil, errors.New("file: no workspace selected")
	}
	files, err := patchutil.Diff(patch, func(path string) (string, error) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		return string(data), err
	})
	if err != nil {
		return nil, err
	}
	out := make([]PatchFile, 0, len(files))
	for _, f := range files {
		pf := PatchFile{Path: f.Path, Action: f.Action, Added: f.Added, Removed: f.Removed}
		for _, l := range f.Lines {
			kind := "context"
			switch l.Kind {
			case patchutil.DiffLineAdd:
				kind = "add"
			case patchutil.DiffLineDelete:
				kind = "delete"
			}
			pf.Lines = append(pf.Lines, PatchLine{
				Kind: kind, OldNum: l.OldNum, NewNum: l.NewNum, Text: l.Text,
			})
		}
		out = append(out, pf)
	}
	return out, nil
}

// SaveArtifactAs copies a workspace artifact via the native save dialog.
func (b *File) SaveArtifactAs(path string) (string, error) {
	full, err := b.resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", full)
	}
	dest, err := wailsruntime.SaveFileDialog(
		b.core.Shell.Context(),
		wailsruntime.SaveDialogOptions{
			DefaultFilename:      filepath.Base(full),
			DefaultDirectory:     filepath.Dir(full),
			CanCreateDirectories: true,
		},
	)
	if err != nil || dest == "" || filepath.Clean(dest) == filepath.Clean(full) {
		return dest, err
	}
	in, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(
		dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return "", copyErr
	}
	return dest, closeErr
}

// OpenArtifactWith opens one artifact through the platform "Open With"
// flow.
func (b *File) OpenArtifactWith(path string) error {
	full, err := b.resolve(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(full)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", full)
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", full).Start()
	case "windows":
		cmd := exec.Command("rundll32", "shell32.dll,OpenAs_RunDLL", full)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	default:
		cmd := exec.Command("xdg-open", full)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
}

// PickFolder opens a native directory picker.
func (b *File) PickFolder(title string) (string, error) {
	return wailsruntime.OpenDirectoryDialog(
		b.core.Shell.Context(),
		wailsruntime.OpenDialogOptions{
			Title:            title,
			DefaultDirectory: b.core.ActiveWorkDir(),
		},
	)
}

// PickFile opens a native file picker.
func (b *File) PickFile(title, pattern string) (string, error) {
	filters := []wailsruntime.FileFilter{}
	if pattern != "" {
		filters = append(filters, wailsruntime.FileFilter{
			DisplayName: "Files",
			Pattern:     pattern,
		})
	}
	return wailsruntime.OpenFileDialog(
		b.core.Shell.Context(),
		wailsruntime.OpenDialogOptions{
			Title:            title,
			DefaultDirectory: b.core.ActiveWorkDir(),
			Filters:          filters,
		},
	)
}
