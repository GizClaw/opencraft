package desktop

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/utils/filetype"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
)

// mediaServer streams workspace files to the webview's media stack over
// a loopback HTTP endpoint.
//
// The Wails asset handler cannot serve them: the frontend is loaded over
// the wails:// scheme (or the Vite dev server in development), and
// WebKit/AVFoundation play media from http(s) or file URLs, not custom
// schemes. Serving through http.ServeContent also gives the player byte
// ranges for free, so a generated video seeks without crossing the IPC
// bridge as base64.
//
// The listener binds 127.0.0.1 on a random port and serves regular
// files through a per-launch random token: workspace files by their
// relative path (a symlink that escapes the root is rejected), plus the
// single files the viewer opened outside the workspace through the
// opaque ids AbsoluteURL mints. Another local page can enumerate
// neither the workspace nor the id table.
type mediaServer struct {
	listener net.Listener
	server   *http.Server
	token    string
	// root resolves the directory tree the server may serve. It is
	// consulted per request so switching workspaces needs no restart.
	root func() string

	// extMu guards the absolute-file table: the id -> path mapping the
	// viewer streams by, its reverse (one id per path), and the mint
	// order the cap evicts from.
	extMu         sync.Mutex
	external      map[string]string
	externalIDs   map[string]string
	externalOrder []string
}

// newMediaServer starts the loopback listener.
func newMediaServer(root func() string) (*mediaServer, error) {
	if root == nil {
		return nil, errors.New("desktop: media server needs a workspace root")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("desktop: listen for media: %w", err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop: close media listener after token failure",
			listener.Close())
		return nil, fmt.Errorf("desktop: media token: %w", err)
	}
	m := &mediaServer{
		listener: listener,
		token:    hex.EncodeToString(secret),
		root:     root,
	}
	m.server = &http.Server{
		Handler:           http.HandlerFunc(m.handle),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		err := m.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			telemetry.WarnErr(context.Background(),
				"desktop: media server stopped", err)
		}
	}()
	return m, nil
}

// URL returns the loopback URL one workspace-relative file is streamed
// from.
func (m *mediaServer) URL(rel string) (string, error) {
	if m == nil {
		return "", errors.New("desktop: media server is not running")
	}
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if rel == "" || rel == "." {
		return "", errors.New("desktop: media URL needs a file path")
	}
	link := url.URL{
		Scheme: "http",
		Host:   m.listener.Addr().String(),
		Path:   "/media/" + m.token + "/" + rel,
	}
	return link.String(), nil
}

// mediaExternalLimit caps how many distinct files the absolute route
// keeps addressable in one launch. A URL is minted per viewed file; the
// cap only keeps a session that opens thousands of files from growing
// the table without bound. An evicted URL 404s, which the viewer turns
// into its plain system-app fallback.
const mediaExternalLimit = 256

// AbsoluteURL returns the loopback URL one local file streams from,
// wherever it lives: the viewer's address for a video or PDF the user
// opened outside the workspace. The id in the URL is minted here and
// stays bound to this one path, so a URL cannot be edited into a
// different file, and asking for the same path again reuses its URL.
func (m *mediaServer) AbsoluteURL(abs string) (string, error) {
	if m == nil {
		return "", errors.New("desktop: media server is not running")
	}
	abs = strings.TrimSpace(abs)
	if abs == "" || !filepath.IsAbs(abs) {
		return "", errors.New("desktop: media URL needs an absolute path")
	}
	m.extMu.Lock()
	defer m.extMu.Unlock()
	if id, ok := m.externalIDs[abs]; ok {
		return m.externalURL(id), nil
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("desktop: media id: %w", err)
	}
	id := hex.EncodeToString(secret)
	if m.external == nil {
		m.external = make(map[string]string)
		m.externalIDs = make(map[string]string)
	}
	m.external[id] = abs
	m.externalIDs[abs] = id
	m.externalOrder = append(m.externalOrder, id)
	if len(m.externalOrder) > mediaExternalLimit {
		oldest := m.externalOrder[0]
		m.externalOrder = m.externalOrder[1:]
		delete(m.externalIDs, m.external[oldest])
		delete(m.external, oldest)
	}
	return m.externalURL(id), nil
}

// externalURL builds the request URL for one minted id. The path half
// only carries the server token; the id travels as a query parameter,
// so the workspace route below cannot be shadowed by it.
func (m *mediaServer) externalURL(id string) string {
	link := url.URL{
		Scheme:   "http",
		Host:     m.listener.Addr().String(),
		Path:     "/media/" + m.token,
		RawQuery: "id=" + id,
	}
	return link.String()
}

// externalPath resolves one minted id back to the file it names.
func (m *mediaServer) externalPath(id string) string {
	m.extMu.Lock()
	defer m.extMu.Unlock()
	return m.external[id]
}

// Close stops the listener and the requests it is serving. It is safe
// on a nil server so callers can close unconditionally.
func (m *mediaServer) Close() {
	if m == nil || m.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.server.Shutdown(ctx); err != nil {
		telemetry.WarnErr(context.Background(),
			"desktop: media server shutdown failed", err)
	}
}

// handle serves one media request: GET/HEAD only, token checked, and
// either a path resolved under the active workspace (symlink escapes
// rejected) or one file minted by AbsoluteURL. http.ServeContent
// answers range requests, so seeking works.
func (m *mediaServer) handle(w http.ResponseWriter, r *http.Request) {
	// The listener is http://127.0.0.1:<port> while the page is a
	// wails:// document (the Vite dev server in development), so every
	// request here is cross-origin. A <video> plays across that boundary
	// on its own, but a fetch does not, and pdf.js reads a PDF — ranges
	// included — through fetch. The token in the path is the capability;
	// the origin the bytes are handed to is not a second one.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	// Content-Range and Accept-Ranges are not CORS-safelisted response
	// headers, and pdf.js decides whether seeking is possible from them.
	w.Header().Set("Access-Control-Expose-Headers",
		"Accept-Ranges, Content-Range, Content-Length")
	if r.Method == http.MethodOptions {
		// A range request stays a simple request (Range is safelisted),
		// so no preflight is expected; answering one costs nothing and
		// keeps a loader that sends it working.
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Range")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/media/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	token, rel, _ := strings.Cut(rest, "/")
	if subtle.ConstantTimeCompare([]byte(token), []byte(m.token)) != 1 {
		http.NotFound(w, r)
		return
	}
	// A file the viewer opened outside the workspace streams by the id
	// its URL was minted with; the path itself never appears in the
	// request (see AbsoluteURL).
	if id := r.URL.Query().Get("id"); id != "" {
		full := m.externalPath(id)
		if full == "" {
			http.NotFound(w, r)
			return
		}
		m.serve(w, r, full)
		return
	}
	root := m.root()
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	full, err := pathsafe.ResolveUnder(root, rel)
	if err != nil || !pathsafe.RealWithin(root, full) {
		http.NotFound(w, r)
		return
	}
	m.serve(w, r, full)
}

// serve sends one regular file's bytes with http.ServeContent, so a
// player or pdf.js seeks through byte ranges. A file that vanished or
// is not a regular file reports as absent.
func (m *mediaServer) serve(w http.ResponseWriter, r *http.Request, full string) {
	file, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"desktop: close media file failed", file.Close())
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	// The content names the type, not the extension: the files streamed
	// here are session output, and a player handed text/plain for an mp4
	// that was saved as .txt refuses to open it. The extension table
	// still backs up the formats the sniffer cannot name.
	w.Header().Set("Content-Type", filetype.OfPath(full).MediaType)
	// Generated files are session output; replaying a stale body for a
	// regenerated path would be worse than re-reading it.
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}
