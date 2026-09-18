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
// The listener binds 127.0.0.1 on a random port and serves only regular
// files under the active workspace, addressed through a per-launch
// random token. Another local page cannot enumerate the workspace, and a
// symlink that escapes the root is rejected.
type mediaServer struct {
	listener net.Listener
	server   *http.Server
	token    string
	// root resolves the directory tree the server may serve. It is
	// consulted per request so switching workspaces needs no restart.
	root func() string
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

// handle serves one media request: GET/HEAD only, token checked, path
// resolved under the active workspace with symlink escapes rejected.
// http.ServeContent answers range requests, so seeking works.
func (m *mediaServer) handle(w http.ResponseWriter, r *http.Request) {
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
	token, rel, ok := strings.Cut(rest, "/")
	if !ok || rel == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(m.token)) != 1 {
		http.NotFound(w, r)
		return
	}
	root := m.root()
	full, err := pathsafe.ResolveUnder(root, rel)
	if err != nil || !pathsafe.RealWithin(root, full) {
		http.NotFound(w, r)
		return
	}
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
