package desktop

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mp4Bytes carries a real ftyp box so the served content type is
// video/mp4 even where the OS MIME table is empty.
func mp4Bytes() []byte {
	data := []byte{0x00, 0x00, 0x00, 0x18}
	data = append(data, []byte("ftypisom")...)
	return append(data, bytes.Repeat([]byte{0x42}, 32)...)
}

func TestMediaServerStreamsWorkspaceFiles(t *testing.T) {
	root := t.TempDir()
	data := mp4Bytes()
	clip := filepath.Join(root, "generated", "clip.mp4")
	if err := os.MkdirAll(filepath.Dir(clip), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clip, data, 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := newMediaServer(func() string { return root })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	link, err := server.URL("generated/clip.mp4")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "video/") {
		t.Errorf("content type = %q, want a video type", got)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("accept-ranges = %q, want bytes", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, data) {
		t.Errorf("body = %d bytes, want %d", len(body), len(data))
	}
}

// TestMediaServerServesRanges pins the reason the endpoint exists: the
// media element seeks by requesting byte ranges.
func TestMediaServerServesRanges(t *testing.T) {
	root := t.TempDir()
	data := mp4Bytes()
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := newMediaServer(func() string { return root })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	link, err := server.URL("clip.mp4")
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, link, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=4-11")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Range"); got !=
		fmt.Sprintf("bytes 4-11/%d", len(data)) {
		t.Errorf("content-range = %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, data[4:12]) {
		t.Errorf("range body = %q, want %q", body, data[4:12])
	}
}

func TestMediaServerRejectsForeignRequests(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), mp4Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := newMediaServer(func() string { return root })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	base := fmt.Sprintf("http://%s/media", server.listener.Addr())

	for _, tc := range []struct {
		name string
		url  string
	}{
		{"wrong token", base + "/deadbeef/clip.mp4"},
		{"traversal", base + "/" + server.token + "/../secret.txt"},
		{"directory", base + "/" + server.token + "/."},
		{"missing", base + "/" + server.token + "/nope.mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(tc.url)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", resp.StatusCode)
			}
		})
	}

	// A symlink inside the workspace must not serve a file outside it.
	link := filepath.Join(root, "escape.mp4")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resp, err := http.Get(base + "/" + server.token + "/escape.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("symlink status = %d, want 404", resp.StatusCode)
	}

	respPost, err := http.Post(base+"/"+server.token+"/clip.mp4", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer respPost.Body.Close()
	if respPost.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("post status = %d, want 405", respPost.StatusCode)
	}
}

// TestMediaServerFollowsWorkspaceSwitch pins that the served root is
// resolved per request rather than captured at construction.
func TestMediaServerFollowsWorkspaceSwitch(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	if err := os.WriteFile(filepath.Join(second, "clip.mp4"), mp4Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	root := first
	server, err := newMediaServer(func() string { return root })
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	link, err := server.URL("clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := http.Get(link); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status in the first workspace = %d, want 404", resp.StatusCode)
		}
	}
	root = second
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status after the switch = %d, want 200", resp.StatusCode)
	}
}
