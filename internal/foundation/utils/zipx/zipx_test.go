package zipx

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The extractor is the one place a package archive is unpacked — both the
// plugin registry and the application registry install through it — so
// these tests are about the archive's shape (where the package sits, what
// it may contain, what it may cost), not about either caller.

const testMarker = "app.yaml"

// writeZip writes one archive out of name → content pairs.
func writeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeZipSized writes one archive holding the marker plus zero-filled
// entries of the named sizes. The zeros compress to almost nothing, so an
// archive that is genuinely over a bound costs a fraction of a second to
// build and a few hundred KB to keep — which is the shape the bound
// exists for.
func writeZipSized(t *testing.T, marker string, sizes map[string]int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	w, err := zw.Create(testMarker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, marker); err != nil {
		t.Fatal(err)
	}
	for name, size := range sizes {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(w, &zeros{left: size}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// zeros reads n zero bytes without holding them.
type zeros struct{ left int64 }

func (z *zeros) Read(p []byte) (int, error) {
	if z.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.left {
		p = p[:z.left]
	}
	for i := range p {
		p[i] = 0
	}
	z.left -= int64(len(p))
	return len(p), nil
}

// tempRoot points this test's temp directory at itself, so an extraction
// root that was not removed is visible to the test: os.MkdirTemp("") is
// what the extractor calls, and that is the directory variable it reads.
// TMPDIR, TMP and TEMP are all set because the platform picks different
// ones.
func tempRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
	return dir
}

// entriesIn names what a directory holds, failing the test when it cannot
// be read. An extraction that left its root behind shows up here.
func entriesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestExtractFindsThePackageAtTheArchiveRoot(t *testing.T) {
	dir, cleanup, err := Extract(writeZip(t, map[string]string{
		testMarker:   "id: hello",
		"layer.yaml": "version: v1",
	}), testMarker)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	defer cleanup()
	raw, err := os.ReadFile(filepath.Join(dir, testMarker))
	if err != nil {
		t.Fatalf("read extracted manifest: %v", err)
	}
	if string(raw) != "id: hello" {
		t.Fatalf("manifest = %q", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "layer.yaml")); err != nil {
		t.Fatalf("layer beside the manifest: %v", err)
	}
}

// TestExtractFindsThePackageUnderOneTopLevelDirectory pins the shape a
// release artifact or a zipped checkout has: the files sit under a
// versioned directory, and the returned path is that directory rather
// than the extraction root.
func TestExtractFindsThePackageUnderOneTopLevelDirectory(t *testing.T) {
	dir, cleanup, err := Extract(writeZip(t, map[string]string{
		"hello-0.1.0/" + testMarker:    "id: hello",
		"hello-0.1.0/layer.yaml":       "version: v1",
		"hello-0.1.0/nodes/hello.js":   "// script",
		"hello-0.1.0/ui/dist/bui.js":   "// bundle",
		"README.md":                    "not part of the package",
		"hello-0.1.0/nodes/extra.yaml": "version: v1",
	}), testMarker)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	defer cleanup()
	if got := filepath.Base(dir); got != "hello-0.1.0" {
		t.Fatalf("package dir = %q, want the directory holding %s", got, testMarker)
	}
	mustExistIn(t, dir, testMarker)
	mustExistIn(t, dir, filepath.Join("nodes", "hello.js"))
	mustExistIn(t, dir, filepath.Join("ui", "dist", "bui.js"))
	mustNotExistIn(t, dir, "README.md")
}

// TestExtractRemovesWhatItUnpacked pins the cleanup contract: the caller
// installs out of the directory and then lets it go, and an extraction
// root that outlives the install is a leak in the user's temp directory.
func TestExtractRemovesWhatItUnpacked(t *testing.T) {
	root := tempRoot(t)
	dir, cleanup, err := Extract(writeZip(t, map[string]string{
		testMarker: "id: hello",
	}), testMarker)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	mustExistIn(t, dir, testMarker)
	cleanup()
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("extraction root %s is still there after cleanup", dir)
	}
	if names := entriesIn(t, root); len(names) != 0 {
		t.Fatalf("temp root holds %v after cleanup", names)
	}
}

func TestExtractRefusesAnEntryThatEscapesTheArchive(t *testing.T) {
	root := tempRoot(t)
	_, _, err := Extract(writeZip(t, map[string]string{
		testMarker:       "id: hello",
		"../outside.txt": "evil",
	}), testMarker)
	if err == nil || !strings.Contains(err.Error(), "escapes archive") {
		t.Fatalf("extract of a traversal entry = %v", err)
	}
	if names := entriesIn(t, root); len(names) != 0 {
		t.Fatalf("a refused archive left %v behind", names)
	}
}

func TestExtractRefusesAnAbsoluteEntry(t *testing.T) {
	_, _, err := Extract(writeZip(t, map[string]string{
		testMarker:      "id: hello",
		"/etc/evil.txt": "evil",
	}), testMarker)
	if err == nil || !strings.Contains(err.Error(), "escapes archive") {
		t.Fatalf("extract of an absolute entry = %v", err)
	}
}

func TestExtractRefusesAnArchiveWithoutTheMarker(t *testing.T) {
	root := tempRoot(t)
	_, _, err := Extract(writeZip(t, map[string]string{
		"dist/index.js": "// bundle",
	}), testMarker)
	if err == nil || !strings.Contains(err.Error(), "zip has no "+testMarker) {
		t.Fatalf("extract of an archive without %s = %v", testMarker, err)
	}
	if names := entriesIn(t, root); len(names) != 0 {
		t.Fatalf("a refused archive left %v behind", names)
	}
}

func TestExtractRefusesAFileThatIsNotAnArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.zip")
	if err := os.WriteFile(path, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Extract(path, testMarker); err == nil ||
		!strings.Contains(err.Error(), "open zip") {
		t.Fatalf("extract of a file that is not a zip = %v", err)
	}
}

// TestExtractRefusesAnEntryOverTheFileBound and the total test below
// bound what an archive may cost. The guard reads the size the entry
// *declares*, before anything is decompressed, which is what makes a zip
// bomb a refusal rather than an out-of-memory kill.
func TestExtractRefusesAnEntryOverTheFileBound(t *testing.T) {
	root := tempRoot(t)
	_, _, err := Extract(writeZipSized(t, "id: hello", map[string]int64{
		"big.bin": MaxFile + 1,
	}), testMarker)
	if err == nil || !strings.Contains(err.Error(), "zip entry too large") {
		t.Fatalf("extract of an entry over the file bound = %v", err)
	}
	if names := entriesIn(t, root); len(names) != 0 {
		t.Fatalf("a refused archive left %v behind", names)
	}
}

func TestExtractRefusesAnArchiveOverTheTotalBound(t *testing.T) {
	// Every entry is at the per-file bound — allowed one at a time — and
	// together they are over the archive's.
	sizes := map[string]int64{}
	for _, name := range []string{"a.bin", "b.bin", "c.bin", "d.bin", "e.bin"} {
		sizes[name] = MaxFile
	}
	root := tempRoot(t)
	_, _, err := Extract(writeZipSized(t, "id: hello", sizes), testMarker)
	if err == nil || !strings.Contains(err.Error(), "zip entry too large") {
		t.Fatalf("extract of an archive over the total bound = %v", err)
	}
	if names := entriesIn(t, root); len(names) != 0 {
		t.Fatalf("a refused archive left %v behind", names)
	}
}

func mustExistIn(t *testing.T, dir, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
		t.Errorf("%s/%s: %v", dir, rel, err)
	}
}

func mustNotExistIn(t *testing.T, dir, rel string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
		t.Errorf("%s/%s is there, want nothing at that path", dir, rel)
	}
}
