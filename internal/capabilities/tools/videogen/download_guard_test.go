package videogen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/workspace"
)

// TestDownloadRefusesPrivateProviderURLs pins the guard on the download
// hop: the URL comes from the provider, so a hostile (or compromised)
// endpoint must not be able to point the tool at loopback or private
// space. A provider running on this machine opts back in explicitly.
func TestDownloadRefusesPrivateProviderURLs(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request,
	) {
		_, _ = w.Write([]byte("video-bytes"))
	}))
	defer srv.Close()

	tool := MustNew(nil, ws, Settings{})
	_, err = tool.download(context.Background(), srv.URL+"/v.mp4")
	if err == nil || !strings.Contains(err.Error(), "blocked by the SSRF guard") {
		t.Fatalf("download error = %v, want SSRF rejection", err)
	}

	// allow_private opens the same path: a local provider is a
	// deliberate configuration, not a hostile URL.
	local := MustNew(nil, ws, Settings{AllowPrivate: true})
	data, err := local.download(context.Background(), srv.URL+"/v.mp4")
	if err != nil {
		t.Fatalf("download with allow_private: %v", err)
	}
	if string(data) != "video-bytes" {
		t.Fatalf("downloaded %q, want video-bytes", data)
	}
}
