package bindings

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/foundation/utils/filetype"
	"github.com/GizClaw/opencraft/internal/foundation/utils/fshidden"
	"github.com/GizClaw/opencraft/internal/foundation/utils/imageutil"
	patchutil "github.com/GizClaw/opencraft/internal/foundation/utils/patch"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
)

// File exposes workspace file browsing operations.
type File struct {
	core *core.Core
	// mediaURL builds the loopback stream URL for one workspace-relative
	// file. nil (tests, headless wiring) leaves video previews
	// metadata-only.
	mediaURL func(rel string) (string, error)
}

// previewTextLimit caps how many bytes ReadPreview returns for one
// text file. Files above the cap report TooLarge and fall back to the
// system app in the UI instead of streaming megabytes through IPC.
const previewTextLimit = 2 << 20

// previewPdfLimit caps one PDF embedded as a data URL for the inline
// PDF viewer. Larger documents fall back to the system app.
const previewPdfLimit = 40 << 20

// readRoot is one containment root the file viewer may read from.
// The workspace root is always present; the data root covers
// conversation media/exports that live outside the workspace but are
// still owned by the app.
type readRoot struct {
	name string
	dir  string
}

// NewFileBinding wires the file binding.
func NewFileBinding(c *core.Core) *File {
	return &File{core: c}
}

// SetMediaURL installs the stream URL builder used for video previews.
func (b *File) SetMediaURL(fn func(rel string) (string, error)) {
	b.mediaURL = fn
}

// FileNode is one entry of the workspace file tree.
type FileNode struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

// List returns one directory level, sorted dirs-first. showHidden adds
// the entries fshidden classifies as hidden (dot-prefixed off Windows,
// Explorer-hidden attributes on it); the setting is the desktop
// document's ui.showHiddenFiles. The agent-facing file tools keep their
// own include_hidden switch.
func (b *File) List(dir string, showHidden bool) ([]FileNode, error) {
	workspace := b.core.ActiveWorkDir()
	if workspace == "" {
		return nil, errors.New("file: no workspace selected")
	}
	full, root, err := b.locatePath(dir, "")
	if err != nil {
		return nil, err
	}
	if root != "workspace" {
		return nil, fmt.Errorf("file: %q is outside the workspace", dir)
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	out := make([]FileNode, 0, len(entries))
	for _, e := range entries {
		if !showHidden && fshidden.Hidden(e) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, FileNode{
			Name: e.Name(),
			Path: filepath.ToSlash(
				relOf(workspace, filepath.Join(full, e.Name()))),
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

// Search returns workspace-relative paths containing query. showHidden
// matches hidden paths too, so the panel's quick-open finds everything
// the tree shows.
func (b *File) Search(query string, limit int, showHidden bool) ([]SearchFileHit, error) {
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
	ctx := b.core.Shell.Context()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		if !showHidden && fshidden.Hidden(d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			telemetry.WarnErr(ctx, "file: resolve search hit failed", err)
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
	telemetry.WarnErr(ctx, "file: search workspace failed", err)
	if hits == nil {
		hits = []SearchFileHit{}
	}
	return hits, nil
}

func (b *File) resolve(path string) (string, error) {
	root := b.core.ActiveWorkDir()
	if root == "" {
		return "", errors.New("file: no workspace selected")
	}
	full, err := pathsafe.ResolveUnder(root, path)
	if err != nil {
		return "", fmt.Errorf("%s is outside the workspace", path)
	}
	return full, nil
}

// readRoots returns the containment roots the viewer may open.
func (b *File) readRoots() []readRoot {
	var roots []readRoot
	if wd := b.core.ActiveWorkDir(); wd != "" {
		roots = append(roots, readRoot{name: "workspace", dir: wd})
	}
	if dd := b.core.DataDir; dd != "" {
		roots = append(roots, readRoot{name: "data", dir: dd})
	}
	// Skill packages live outside both roots (user/builtin scopes), so
	// document-relative references inside a SKILL.md resolve against
	// the service's registered scan roots.
	if h := b.core.ActiveHost(); h != nil &&
		h.Controller() != nil && h.Controller().Runtime() != nil {
		if value, ok := h.Controller().Runtime().Resource("skills"); ok {
			if svc, ok := value.(*skills.Service); ok && svc != nil {
				for _, root := range svc.Roots() {
					roots = append(roots, readRoot{name: "skill", dir: root})
				}
			}
		}
	}
	return roots
}

// relOf returns full as a slash-separated path relative to root,
// falling back to the cleaned absolute path when the relation cannot
// be computed.
func relOf(root, full string) string {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return filepath.ToSlash(full)
	}
	return filepath.ToSlash(rel)
}

// locatePath resolves a viewer target under containment: relative
// targets resolve under base ("" = workspace root), absolute targets
// must land inside one registered read root. Symlinks are evaluated
// before the containment check so a link cannot smuggle reads outside
// the roots.
func (b *File) locatePath(target, base string) (string, string, error) {
	if strings.TrimSpace(target) == "" {
		return "", "", fmt.Errorf("file: empty path")
	}
	full := target
	if !filepath.IsAbs(full) {
		wd := b.core.ActiveWorkDir()
		if wd == "" {
			return "", "", errors.New("file: no workspace selected")
		}
		dir := wd
		if base != "" {
			if filepath.IsAbs(base) {
				dir = base
			} else {
				dir = filepath.Join(wd, filepath.FromSlash(base))
			}
		}
		full = filepath.Join(dir, filepath.FromSlash(target))
	}
	full = filepath.Clean(full)
	eval, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", "", fmt.Errorf("file: resolve %q: %w", target, err)
	}
	for _, rr := range b.readRoots() {
		rootEval, err := filepath.EvalSymlinks(rr.dir)
		if err != nil {
			continue
		}
		if pathsafe.Within(rootEval, eval) {
			// Keep the caller-visible path (not the symlink-evaluated
			// one): on macOS the workspace root is commonly reachable
			// through /var while EvalSymlinks normalizes it to
			// /private/var, and Rel() against the original root would
			// otherwise produce a useless escaped-looking path.
			return full, rr.name, nil
		}
	}
	return "", "", fmt.Errorf("file: %q is outside the readable roots", target)
}

// ResolvedTarget describes one containment-checked local target for
// the file viewer and the link router.
type ResolvedTarget struct {
	Path      string `json:"path"` // absolute resolved path
	Rel       string `json:"rel"`  // workspace-relative display path
	Root      string `json:"root"` // "workspace" | "data" | "skill"
	Name      string `json:"name"`
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// ResolveTarget resolves a viewer target (workspace-relative by
// default, absolute for conversation media/exports) under containment.
func (b *File) ResolveTarget(target, base string) (ResolvedTarget, error) {
	full, root, err := b.locatePath(target, base)
	if err != nil {
		return ResolvedTarget{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return ResolvedTarget{}, fmt.Errorf("file: stat %q: %w", target, err)
	}
	rel := ""
	if root == "workspace" {
		rel = relOf(b.core.ActiveWorkDir(), full)
	}
	return ResolvedTarget{
		Path:      full,
		Rel:       rel,
		Root:      root,
		Name:      filepath.Base(full),
		IsDir:     info.IsDir(),
		Size:      info.Size(),
		MediaType: filetype.OfPath(full).MediaType,
	}, nil
}

// FilePreview is the bounded preview payload returned by ReadPreview.
// Previewable files carry Text or DataURL; everything else returns
// metadata only so the UI can offer the system-app fallback.
type FilePreview struct {
	Path      string `json:"path"`
	Rel       string `json:"rel"`
	Root      string `json:"root"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
	// Kind tells the UI how to render the payload: text, image, pdf,
	// video (played from StreamURL), or meta (no inline preview; offer
	// system-app actions).
	Kind    string `json:"kind"`
	Text    string `json:"text,omitempty"`
	DataURL string `json:"data_url,omitempty"`
	// StreamURL is the loopback URL a video plays from. It is only set
	// for files under the workspace root; the media element streams and
	// seeks through byte ranges, so no size cap applies.
	StreamURL string `json:"stream_url,omitempty"`
	TooLarge  bool   `json:"too_large"`
	// MtimeNS is the modification stamp of the file this payload was
	// read from. The viewer compares it against Git.FileMarks' stamp to
	// notice an out-of-band write and reload silently.
	MtimeNS int64 `json:"mtime_ns"`
}

// ReadPreview returns a bounded preview for one viewer target: text
// (capped at previewTextLimit), small/normalizable images as data
// URLs, or bare metadata for formats the viewer does not render.
// The file's type comes from its content (see the filetype package),
// so a source file named .ts opens in the code viewer while a
// transport stream beside it plays as video.
func (b *File) ReadPreview(path string) (FilePreview, error) {
	full, root, err := b.locatePath(path, "")
	if err != nil {
		return FilePreview{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return FilePreview{}, fmt.Errorf("file: stat %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return FilePreview{}, fmt.Errorf("file: %q is not a regular file", path)
	}
	kind := filetype.OfPath(full)
	mediaType := kind.MediaType
	rel := ""
	if root == "workspace" {
		rel = relOf(b.core.ActiveWorkDir(), full)
	}
	out := FilePreview{
		Path:      full,
		Rel:       rel,
		Root:      root,
		Name:      filepath.Base(full),
		Size:      info.Size(),
		MediaType: mediaType,
		Kind:      "meta",
		MtimeNS:   info.ModTime().UnixNano(),
	}
	// Video and PDF come back as a loopback URL rather than base64. The
	// viewer's own loader then reads what it needs (a player seeks
	// through ranges, pdf.js fetches the pages it is about to draw)
	// instead of paying for a base64 copy in Go, a second one across the
	// IPC bridge, and a third in the renderer's heap — which for a PDF is
	// what the 40 MiB preview cap used to buy.
	if url := b.streamURL(root, rel, mediaType); url != "" {
		out.Kind = previewKind(mediaType)
		out.StreamURL = url
		return out, nil
	}
	if info.Size() > previewTextLimit {
		if kind.Text {
			out.TooLarge = true
			return out, nil
		}
		dataURL, err := previewDataURL(full, mediaType, info.Size())
		if err == nil {
			out.DataURL = dataURL
			out.Kind = previewKind(mediaType)
		} else if previewableMediaType(mediaType) {
			out.TooLarge = true
		}
		return out, nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return FilePreview{}, fmt.Errorf("file: read %q: %w", path, err)
	}
	// The sample decided text; the whole payload confirms it, and a NUL
	// past the sample demotes the file to the metadata pane.
	if kind.Text && filetype.IsText(data) {
		out.Text = string(data)
		out.Kind = "text"
		return out, nil
	}
	dataURL, err := previewDataURL(full, mediaType, info.Size())
	if err == nil {
		out.DataURL = dataURL
		out.Kind = previewKind(mediaType)
	} else if previewableMediaType(mediaType) {
		out.TooLarge = true
	}
	return out, nil
}

// streamURL returns the loopback URL a video or PDF preview loads from,
// or "" when the file is not workspace media or streaming is
// unavailable.
func (b *File) streamURL(root, rel, mediaType string) string {
	if b.mediaURL == nil || root != "workspace" || rel == "" {
		return ""
	}
	if filetype.Family(mediaType) != "video" &&
		mediaType != "application/pdf" {
		return ""
	}
	url, err := b.mediaURL(rel)
	if err != nil {
		return ""
	}
	return url
}

// previewableMediaType reports whether the viewer tries to embed this
// type inline (image or PDF); everything else is a metadata-only file.
func previewableMediaType(mediaType string) bool {
	family := filetype.Family(mediaType)
	return family == "image" || family == "pdf"
}

// previewKind maps a previewable media type to the UI kind. Video only
// ever arrives here through the streaming path — it has no inline form —
// and the viewer renders it from StreamURL the same way it renders a
// PDF.
func previewKind(mediaType string) string {
	family := filetype.Family(mediaType)
	if family == "image" || family == "pdf" || family == "video" {
		return family
	}
	return "meta"
}

// previewDataURL embeds an inline-previewable payload (image or PDF)
// as a data URL. Unsupported types return an error so the caller marks
// the file as not previewable instead of failing the whole request.
func previewDataURL(path, mediaType string, size int64) (string, error) {
	if filetype.Family(mediaType) == "image" {
		return imagePreviewDataURL(path, mediaType, size)
	}
	if mediaType == "application/pdf" && size <= previewPdfLimit {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return "data:application/pdf;base64," +
			base64.StdEncoding.EncodeToString(data), nil
	}
	return "", fmt.Errorf("file: %s is not inline-previewable", mediaType)
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
// Workspace-relative and absolute containment-root paths are both
// accepted so conversation media can be handed to the system app.
func (b *File) OpenPath(path string) error {
	full, _, err := b.locatePath(path, "")
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
	telemetry.WarnErr(b.core.Shell.Context(),
		"file: release open command failed", cmd.Process.Release())
	return nil
}

// Reveal highlights a path in the platform file manager.
func (b *File) Reveal(path string) error {
	full, _, err := b.locatePath(path, "")
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
	telemetry.WarnErr(b.core.Shell.Context(),
		"file: release reveal command failed", cmd.Process.Release())
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

// Attachment preview metadata. MediaType describes the source file;
// DataURL may carry a different media type when the preview was
// normalized (see imagePreviewDataURL), so consumers must not assume
// the two agree.
type Attachment struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
	DataURL   string `json:"data_url,omitempty"`
}

// ReadAttachment returns preview metadata; images include a data URL.
func (b *File) ReadAttachment(path string) (Attachment, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Attachment{}, errors.New("attachment path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return Attachment{}, err
	}
	if !info.Mode().IsRegular() {
		return Attachment{}, fmt.Errorf("%s is not a regular file", path)
	}
	mediaType := filetype.OfPath(path).MediaType
	dto := Attachment{
		Name:      filepath.Base(path),
		Path:      path,
		Size:      info.Size(),
		MediaType: mediaType,
	}
	if filetype.Family(mediaType) == "image" {
		dataURL, err := imagePreviewDataURL(path, mediaType, info.Size())
		if err != nil {
			return Attachment{}, err
		}
		dto.DataURL = dataURL
	}
	return dto, nil
}

// imagePreviewDataURL returns the base64 data URL shown for one local
// image. Sources that already fit the preview cap are embedded
// byte-for-byte, so small images never go through a lossy round trip
// and browsers keep applying EXIF orientation themselves. Larger
// decodable still images are normalized to JPEG q90 (EXIF applied,
// transparency flattened) so a big PNG/TIFF/BMP can still preview.
// Formats that cannot be normalized (WebP/AVIF, animated GIF, corrupt
// files) are rejected once they exceed the cap.
func imagePreviewDataURL(path, mediaType string, size int64) (string, error) {
	if size <= imageutil.MaxInlineImageBytes {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return "data:" + mediaType + ";base64," +
			base64.StdEncoding.EncodeToString(data), nil
	}
	if mediaType != "image/gif" {
		if data, err := imageutil.NormalizeFileToJPEG(path); err == nil &&
			int64(len(data)) <= imageutil.MaxInlineImageBytes {
			return "data:image/jpeg;base64," +
				base64.StdEncoding.EncodeToString(data), nil
		}
	}
	return "", fmt.Errorf(
		"image too large to preview (%d bytes)", size)
}

// maxPastedImageBytes caps one clipboard image materialized by
// ImportPastedImage. Screenshots are far below this; the cap keeps
// the IPC payload and the temporary staging file bounded.
const maxPastedImageBytes = 20 << 20

// pasteImageExts maps clipboard media types to staging file
// extensions. Everything outside this set is rejected up front, so a
// pasted blob can never be staged under a misleading extension.
var pasteImageExts = map[string]string{
	"image/avif":    "avif",
	"image/bmp":     "bmp",
	"image/gif":     "gif",
	"image/heic":    "heic",
	"image/jpeg":    "jpg",
	"image/png":     "png",
	"image/svg+xml": "svg",
	"image/tiff":    "tiff",
	"image/webp":    "webp",
}

// ImportPastedImage materializes an image pasted from the system
// clipboard (base64 data URL) into a temporary local file. The rest
// of the attachment pipeline (preview, session persistence, prompt
// inlining) then treats it exactly like a picked file.
func (b *File) ImportPastedImage(name, dataURL string) (Attachment, error) {
	data, mediaType, err := decodeImageDataURL(dataURL)
	if err != nil {
		return Attachment{}, err
	}
	ext, ok := pasteImageExts[mediaType]
	if !ok {
		return Attachment{}, fmt.Errorf(
			"file: unsupported clipboard image type %q", mediaType)
	}
	f, err := os.CreateTemp("", "opencraft-paste-*."+ext)
	if err != nil {
		return Attachment{}, fmt.Errorf(
			"file: create pasted image temp file: %w", err)
	}
	path := f.Name()
	remove := func() {
		telemetry.WarnErr(context.Background(),
			"file: remove failed pasted image", os.Remove(path))
	}
	if _, err := f.Write(data); err != nil {
		closeErr := f.Close()
		telemetry.WarnErr(context.Background(),
			"file: close failed pasted image", closeErr)
		remove()
		return Attachment{}, fmt.Errorf("file: write pasted image: %w", err)
	}
	if err := f.Close(); err != nil {
		remove()
		return Attachment{}, fmt.Errorf("file: close pasted image: %w", err)
	}
	att, err := b.ReadAttachment(path)
	if err != nil {
		remove()
		return Attachment{}, err
	}
	att.Name = pastedAttachmentName(name, ext)
	return att, nil
}

// decodeImageDataURL parses a base64 image data URL and enforces the
// decoded-size cap before anything is written to disk.
func decodeImageDataURL(dataURL string) ([]byte, string, error) {
	const dataPrefix = "data:"
	const base64Marker = ";base64,"
	if !strings.HasPrefix(dataURL, dataPrefix) {
		return nil, "", fmt.Errorf("file: pasted image must be a data URL")
	}
	rest := dataURL[len(dataPrefix):]
	marker := strings.Index(rest, base64Marker)
	if marker < 0 {
		return nil, "", fmt.Errorf(
			"file: pasted image must be base64 encoded")
	}
	mediaType := strings.ToLower(strings.TrimSpace(rest[:marker]))
	if filetype.Family(mediaType) != "image" {
		return nil, "", fmt.Errorf(
			"file: pasted payload is not an image (%q)", mediaType)
	}
	data, err := base64.StdEncoding.DecodeString(rest[marker+len(base64Marker):])
	if err != nil {
		return nil, "", fmt.Errorf("file: decode pasted image: %w", err)
	}
	if len(data) > maxPastedImageBytes {
		return nil, "", fmt.Errorf(
			"file: pasted image too large (%d bytes)", len(data))
	}
	return data, mediaType, nil
}

// pastedAttachmentName returns the display name for a pasted image:
// the caller-supplied name when sane, otherwise "clipboard.<ext>".
// The stored extension always matches the actual media type.
func pastedAttachmentName(name, ext string) string {
	name = strings.TrimSpace(name)
	name = filepath.Base(filepath.FromSlash(name))
	if name == "" || name == "." || name == ".." {
		name = "clipboard." + ext
	}
	return name
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
		pf := PatchFile{
			Path:    f.Path,
			Action:  f.Action,
			Added:   f.Added,
			Removed: f.Removed,
			Lines:   []PatchLine{},
		}
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
	dest, err := b.core.Shell.SaveFileDialog(
		filepath.Base(full), filepath.Dir(full))
	if err != nil || dest == "" || filepath.Clean(dest) == filepath.Clean(full) {
		return dest, err
	}
	in, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer func() {
		telemetry.WarnErr(b.core.Shell.Context(),
			"file: close source after artifact copy failed", in.Close())
	}()
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
	full, _, err := b.locatePath(path, "")
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
	return b.core.Shell.OpenDirectoryDialog(title, b.core.ActiveWorkDir())
}

// PickFile opens a native file picker.
func (b *File) PickFile(title, pattern string) (string, error) {
	return b.core.Shell.OpenFileDialog(title, b.core.ActiveWorkDir(), pattern)
}
