package apps

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/foundation/utils/netguard"
)

// TestSourceVocabularyIsExplicit: every source the host accepts is
// spelled out, and everything else is refused with the spelling that
// would have worked. The reports are what a wizard shows, so each one
// names the input and what is wrong with it — a guess ("this looks like
// a git host") would fetch somewhere the user did not name.
func TestSourceVocabularyIsExplicit(t *testing.T) {
	store, _, _ := newStore(t)
	ctx := context.Background()
	cases := []struct {
		name string
		src  string
		want string
	}{
		{name: "nothing typed", src: "   ",
			want: "source is required"},
		{name: "plaintext http is not fetched",
			src:  "http://packages.example.com/hello.zip",
			want: "http is only allowed for private or test hosts"},
		{name: "credentials travel in the URL",
			src:  "https://user:secret@packages.example.com/hello.zip",
			want: "without credentials or fragment"},
		{name: "a fragment is not part of an archive URL",
			src:  "https://packages.example.com/hello.zip#main",
			want: "without credentials or fragment"},
		{name: "a git remote written without the prefix",
			src:  "https://github.com/owner/hello.git",
			want: "is a git remote: write it git+https://github.com/owner/hello.git to clone it"},
		{name: "ssh is not something the host can prompt for",
			src:  "git+ssh://git@github.com/owner/hello.git",
			want: "the host does not run ssh"},
		{name: "a scheme git cannot use",
			src:  "git+ftp://example.com/hello.git",
			want: `unsupported scheme "ftp"`},
		// A repository on this machine is a path, and git's file
		// transport ignores the host half: a pasted
		// "git+file://git.corp/team/app.git" would read the local
		// "/team/app.git" or fail on it, never the host it named.
		{name: "a file URL that names a host",
			src:  "git+file://git.example.com/team/hello.git",
			want: "file:// names a repository on this machine"},
		{name: "a host pasted without its scheme",
			src:  "github.com/owner/hello",
			want: "a remote source is written git+https://host/owner/repo.git or https://host/package.zip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Inspect(ctx, tc.src)
			if err == nil {
				t.Fatalf("Inspect(%q) was accepted", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Inspect(%q) = %v, want it to read %q", tc.src, err, tc.want)
			}
		})
	}
}

// TestInspectClonesAGitSourceAtTheRef: a git source is a tree at one
// version, and the ref is what picks it — the default branch when
// nothing is named, the branch or tag after the '#'. The clone happens
// outside the registry (nothing of it survives the call) and the
// repository itself is not part of the package.
func TestInspectClonesAGitSourceAtTheRef(t *testing.T) {
	store, _, _ := newStore(t)
	ctx := context.Background()
	repo := gitRepo(t, newApp(t))
	// A branch left behind at the first version, so the refs below name
	// different trees: a default-branch clone reporting 0.2.0 for all of
	// them would be a test that passes without reading the ref at all.
	gitOut(t, repo, "branch", "v1")
	gitWrite(t, repo, map[string]string{
		ManifestFile: strings.Replace(fixtureManifest, "version: 0.1.0", "version: 0.2.0", 1),
	})
	gitOut(t, repo, "commit", "-m", "second")
	gitOut(t, repo, "tag", "v2")

	defaultBranch := gitOut(t, repo, "branch", "--show-current")
	ref := "file://" + repo
	cases := []struct {
		name string
		src  string
		want string
	}{
		{name: "the default branch",
			src: "git+" + ref, want: "0.2.0"},
		{name: "a named branch",
			src: "git+" + ref + "#" + defaultBranch, want: "0.2.0"},
		{name: "a tag",
			src: "git+" + ref + "#v2", want: "0.2.0"},
		{name: "a branch that is not the default",
			src: "git+" + ref + "#v1", want: "0.1.0"},
		{name: "a ref that is not there",
			src:  "git+" + ref + "#nope",
			want: "git clone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			insp, err := store.Inspect(ctx, tc.src)
			if tc.want == "git clone" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("Inspect(%q) = %v, want a clone failure", tc.src, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Inspect(%q): %v", tc.src, err)
			}
			if insp.Summary.Version != tc.want {
				t.Fatalf("Inspect(%q) version = %q, want %q",
					tc.src, insp.Summary.Version, tc.want)
			}
		})
	}
}

// TestInstallFromAGitSourceLeavesTheRepositoryBehind: the content root
// gets the tree, not the history — and not the remote's branch config
// either, so a locally installed application is not a repository with an
// origin pointing at whoever packaged it.
func TestInstallFromAGitSourceLeavesTheRepositoryBehind(t *testing.T) {
	store, root, _ := newStore(t)
	repo := gitRepo(t, newApp(t))
	sum, err := store.Install(
		context.Background(),
		"git+file://"+repo,
		InstallOptions{ID: "cloned"},
	)
	if err != nil {
		t.Fatalf("install from a git source: %v", err)
	}
	if !sum.Enabled {
		t.Fatalf("summary = %+v, want it enabled", sum)
	}
	content := filepath.Join(root, "cloned", "content")
	mustExist(t, filepath.Join(content, "layer.yaml"))
	mustNotExist(t, filepath.Join(content, ".git"))
}

// TestInspectDownloadsAnArchiveURL: the URL half of the vocabulary is a
// download of the same archive shape a picked file is (a GitHub
// checkout, app.yaml under the single top-level directory), and it is
// fetched under the shared guards — a test server is loopback, so the
// default policy is the case that refuses and the explicit one is the
// case that installs.
func TestInspectDownloadsAnArchiveURL(t *testing.T) {
	ctx := context.Background()
	dir := newApp(t)
	archive := zipTree(t, dir, "hello-0.1.0/")
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/hello.zip", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/missing.zip", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	})
	mux.HandleFunc("/page.zip", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>404 of the HTML kind</html>")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	guarded, _, _ := newStore(t)
	if _, err := guarded.Inspect(ctx, ts.URL+"/hello.zip"); err == nil ||
		!strings.Contains(err.Error(), "http is only allowed for private or test hosts") {
		t.Fatalf("the default policy fetched a plaintext loopback URL: %v", err)
	}

	store, _, _ := newStore(t)
	store.sourcePolicy = netguard.Policy{AllowPrivate: true}
	insp, err := store.Inspect(ctx, ts.URL+"/hello.zip")
	if err != nil {
		t.Fatalf("inspect an archive URL: %v", err)
	}
	if insp.Summary.ID != "hello" {
		t.Fatalf("summary = %+v", insp.Summary)
	}
	if len(insp.Refusals) != 0 {
		t.Fatalf("refusals = %+v, want a package the archive carried clean", insp.Refusals)
	}
	if _, err := store.Inspect(ctx, ts.URL+"/missing.zip"); err == nil ||
		!strings.Contains(err.Error(), "returned 404") {
		t.Fatalf("inspect a missing archive = %v", err)
	}
	if _, err := store.Inspect(ctx, ts.URL+"/page.zip"); err == nil ||
		!strings.Contains(err.Error(), "is not a zip archive") {
		t.Fatalf("inspect an HTML page named .zip = %v", err)
	}
}

// TestDownloadBoundCountsBytesNotClaims: the bound on a downloaded
// archive is on the bytes that arrive, so a server cannot talk the host
// past it — neither by omitting a Content-Length (this response is
// chunked: nothing states a size) nor by understating one. The bound is
// lowered here because the shipped one is 256 MiB, and the answer has to
// be the same whichever number it is.
func TestDownloadBoundCountsBytesNotClaims(t *testing.T) {
	streamed := make([]byte, 3<<19) // 1.5 MiB, past the 1 MiB bound below
	mux := http.NewServeMux()
	mux.HandleFunc("/big.zip", func(w http.ResponseWriter, _ *http.Request) {
		// Flush before writing, so the response is chunked and carries no
		// Content-Length: a host that read the header and trusted it
		// would see no bound at all.
		w.(http.Flusher).Flush()
		for off := 0; off < len(streamed); off += 64 << 10 {
			end := min(off+(64<<10), len(streamed))
			if _, err := w.Write(streamed[off:end]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	store, root, _ := newStore(t)
	store.sourcePolicy = netguard.Policy{AllowPrivate: true}
	store.sourceBytes = 1 << 20
	_, err := store.Inspect(context.Background(), ts.URL+"/big.zip")
	if err == nil || !strings.Contains(err.Error(),
		"is larger than the 1.0 MiB one package may be") {
		t.Fatalf("inspect an oversized download = %v", err)
	}
	// And nothing of it reached the registry: the bound is checked before
	// the archive is read at all.
	mustBeEmpty(t, root)
}

// TestDownloadStopsReadingAtTheBound: reaching the bound ends the
// transfer, it does not merely refuse what already arrived. The response
// here never ends — a server that streams forever is the case this rule
// exists for — so a host that read it all would sit on the socket until
// its context gave out instead of reporting the bound.
func TestDownloadStopsReadingAtTheBound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/endless.zip", func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		flusher.Flush() // chunked: nothing states a size
		chunk := make([]byte, 64<<10)
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			flusher.Flush()
			if r.Context().Err() != nil {
				return
			}
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	store, _, _ := newStore(t)
	store.sourcePolicy = netguard.Policy{AllowPrivate: true}
	store.sourceBytes = 1 << 20
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := store.Inspect(ctx, ts.URL+"/endless.zip")
	if err == nil || !strings.Contains(err.Error(),
		"is larger than the 1.0 MiB one package may be") {
		t.Fatalf("inspect an endless download = %v", err)
	}
}

// TestResolveGitStagesATreeWithoutItsRepository: the staging directory a
// git source resolves to holds the tree and nothing else, and the ref
// travelling with the remote is the ref, not part of the path.
//
// This is where the rule can be seen at all: an install's content root
// is copied by copyTree, which skips dot entries, so a repository left
// in the staging tree would be invisible to the end-to-end install test
// below — and invisible until someone stops filtering, at which point an
// application's content root is a git checkout.
func TestResolveGitStagesATreeWithoutItsRepository(t *testing.T) {
	store, _, _ := newStore(t)
	repo := gitRepo(t, newApp(t))
	res, err := store.resolveSource(
		context.Background(), "git+file://"+repo+"#main")
	if err != nil {
		t.Fatalf("resolve a git source: %v", err)
	}
	defer res.cleanup()
	if res.kind != sourceGit {
		t.Fatalf("kind = %q, want %q", res.kind, sourceGit)
	}
	if res.ref != "main" || !strings.HasSuffix(res.remote, repo) {
		t.Fatalf("resolved %+v, want the main branch of %s", res, repo)
	}
	mustExist(t, filepath.Join(res.dir, ManifestFile))
	mustNotExist(t, filepath.Join(res.dir, ".git"))
}

// gitRepo copies one package tree into a fresh repository, commits it,
// and returns the repository directory. The identity is per command (the
// developer's own git configuration is not the test's business) and
// hooks are disabled, so a machine with a global hooks path cannot make
// this fail.
func gitRepo(t *testing.T, tree string) string {
	t.Helper()
	dir := t.TempDir()
	gitOut(t, dir, "init", "-b", "main")
	if err := copyTree(tree, dir); err != nil {
		t.Fatal(err)
	}
	gitWrite(t, dir, nil)
	gitOut(t, dir, "commit", "-m", "initial")
	return dir
}

// gitWrite writes one tree into repo and stages it, so the next git
// command in the test sees a working tree with something to commit.
func gitWrite(t *testing.T, repo string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitOut(t, repo, "add", "-A")
}

// gitOut runs one git command in repo and returns its stdout, failing
// the test with the combined output when git refuses.
func gitOut(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git",
		append([]string{
			"-C", repo,
			"-c", "user.name=OpenCraft Tests",
			"-c", "user.email=tests@opencraft.invalid",
			"-c", "commit.gpgsign=false",
			"-c", "core.hooksPath=",
		}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
