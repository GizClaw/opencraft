package apps

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/utils/zipx"
)

// Inspecting a candidate is the wizard's half of the registry: it answers
// "what would installing this do" without copying anything, and it
// answers a refused package with the rows a user can act on instead of an
// error. Reading an installed application's content files is the page's
// half: the frontend module and the stylesheet arrive through here, and
// nothing else does.

// newAppWithUI writes the fixture application plus the frontend bundle an
// installed application serves to the page.
func newAppWithUI(t *testing.T) string {
	t.Helper()
	dir := newApp(t)
	writeTestFile(t, dir, ManifestFile, fixtureManifest+
		"icon: icon.png\nui:\n  entry: ui/dist/index.js\n  style: ui/dist/index.css\n")
	writeTestFile(t, dir, "icon.png", "not really a png, but a file")
	writeTestFile(t, dir, "ui/dist/index.js", "export function apply() {}\n")
	writeTestFile(t, dir, "ui/dist/index.css", ".hello { color: red }\n")
	return dir
}

// installWithUI installs the frontend-carrying fixture and returns the
// store it landed in.
func installWithUI(t *testing.T) *Store {
	t.Helper()
	store, _, _ := newStore(t)
	install(t, store, newAppWithUI(t))
	return store
}

// snapshotTree reads one directory into rel → contents, so a test can
// assert that a path was not written to.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// mustBeUntouched asserts that a directory holds exactly what the
// snapshot captured: inspecting a package may not edit the user's copy.
func mustBeUntouched(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	after := snapshotTree(t, dir)
	if len(after) != len(before) {
		t.Fatalf("%s holds %d files, want the %d it held", dir, len(after), len(before))
	}
	for rel, want := range before {
		if got, ok := after[rel]; !ok || got != want {
			t.Errorf("%s/%s changed: %q", dir, rel, got)
		}
	}
}

// TestInspectAcceptsAPackageAndDescribesTheInstall pins the card the
// wizard previews from an untouched source.
func TestInspectAcceptsAPackageAndDescribesTheInstall(t *testing.T) {
	store, root, _ := newStore(t)
	insp, err := store.Inspect(context.Background(), newApp(t))
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(insp.Refusals) != 0 {
		t.Fatalf("refusals = %v, want none", insp.Refusals)
	}
	if insp.Summary.ID != "hello" || insp.Summary.Name != "Hello" ||
		insp.Summary.Version != "0.1.0" || insp.Summary.Agent != "app" {
		t.Fatalf("summary = %+v", insp.Summary)
	}
	if !insp.Summary.Enabled {
		t.Error("a candidate reports the state an install leaves it in, which is enabled")
	}
	if len(insp.Layers) != 1 || insp.Layers[0] != "layer.yaml" {
		t.Fatalf("layers = %v", insp.Layers)
	}
	mustBeEmpty(t, root)
}

// TestInspectReportsRefusalsAsData pins the wizard's contract: a package
// that cannot be installed is a verdict with one row per problem, not an
// error, because the rows are what the user has to fix.
func TestInspectReportsRefusalsAsData(t *testing.T) {
	src := newApp(t)
	writeTestFile(t, src, "layer.yaml", `resources:
  events:
    kind: event.Bus
    impl: memory
agents:
  app:
    card:
      name: Hello
    engine:
      settings:
        graph: { file: graph.yaml }
`)
	store, root, _ := newStore(t)
	insp, err := store.Inspect(context.Background(), src)
	if err != nil {
		t.Fatalf("inspect of a refused package = %v, want the refusals", err)
	}
	if len(insp.Refusals) != 1 {
		t.Fatalf("refusals = %v, want one row", insp.Refusals)
	}
	got := insp.Refusals[0]
	if got.Key != "resources.events" || got.Layer != "layer.yaml" {
		t.Fatalf("refusal = %+v", got)
	}
	if !strings.Contains(got.Reason, "the host provides") {
		t.Fatalf("refusal reason = %q", got.Reason)
	}
	if insp.Summary.ID != "hello" {
		t.Fatalf("a refused package still describes itself: %+v", insp.Summary)
	}
	mustBeEmpty(t, root)
}

// TestInspectErrorsWhenThereIsNoManifestToReport pins where inspection
// stops reporting rows: a manifest the host cannot read is not a package
// with problems, it is a directory that is not (yet) an application — the
// import path that writes a manifest for one is a separate step.
func TestInspectErrorsWhenThereIsNoManifestToReport(t *testing.T) {
	store, _, _ := newStore(t)
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, dir string)
		want  string
	}{
		{
			name:  "no manifest at all",
			write: func(t *testing.T, dir string) {},
			want:  "app.yaml is missing",
		},
		{
			name: "a layer where the manifest belongs",
			write: func(t *testing.T, dir string) {
				writeTestFile(t, dir, "app.yaml", "version: v1\n")
			},
			want: "not an application manifest",
		},
		{
			name: "an id that is not one",
			write: func(t *testing.T, dir string) {
				writeTestFile(t, dir, "app.yaml",
					"app: v1\nid: Hello ../World\nname: Hello\nversion: 0.1.0\nlayers: [layer.yaml]\n")
			},
			want: `id "Hello ../World" is not an application id`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.write(t, dir)
			_, err := store.Inspect(context.Background(), dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("inspect = %v, want %q", err, tc.want)
			}
			var refusals Refusals
			if errors.As(err, &refusals) {
				t.Fatalf("a manifest that cannot be read is an error, not refusals: %v", err)
			}
		})
	}
}

func TestInspectChecksTheManifestAgainstTheHostVersion(t *testing.T) {
	src := newApp(t)
	writeTestFile(t, src, ManifestFile, fixtureManifest+"minHostVersion: 2.0.0\n")
	store, _, _ := newStore(t)
	_, err := store.Inspect(context.Background(), src)
	if err == nil || !strings.Contains(err.Error(), "requires host 2.0.0, running 0.1.0") {
		t.Fatalf("inspect of a package for a newer host = %v", err)
	}
}

// TestInspectReadsTheSourceAndNothingElse pins what inspection is: a read
// of the user's directory with no copy, no staging and no state — the
// same directory is inspected again by the install that follows it.
func TestInspectReadsTheSourceAndNothingElse(t *testing.T) {
	src := newApp(t)
	before := snapshotTree(t, src)
	store, root, dataDir := newStore(t)
	for _, dir := range []string{src} {
		if _, err := store.Inspect(context.Background(), dir); err != nil {
			t.Fatalf("inspect: %v", err)
		}
	}
	mustBeUntouched(t, src, before)
	mustBeEmpty(t, root)
	mustNotExist(t, filepath.Join(dataDir, "apps"))
}

func TestInspectRefusesWhatItCannotRead(t *testing.T) {
	store, _, _ := newStore(t)
	file := filepath.Join(t.TempDir(), "package.zip")
	if err := os.WriteFile(file, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	install(t, store, newApp(t))
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"a directory that is not there", filepath.Join(t.TempDir(), "gone"),
			"source: "},
		{"a file that is not an archive", file, "is not a zip archive"},
		{"nothing at all", "  ", "source is required"},
		{"the registry itself", filepath.Join(store.root, "hello", "content"),
			"is inside the application root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Inspect(context.Background(), tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("inspect = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReadAssetServesTheContentRootsFiles(t *testing.T) {
	store := installWithUI(t)
	raw, err := store.ReadAsset("hello", "ui/dist/index.js")
	if err != nil {
		t.Fatalf("read asset: %v", err)
	}
	if string(raw) != "export function apply() {}\n" {
		t.Fatalf("asset = %q", raw)
	}
	raw, err = store.ReadAsset("hello", "ui/dist/index.css")
	if err != nil {
		t.Fatalf("read stylesheet: %v", err)
	}
	if string(raw) != ".hello { color: red }\n" {
		t.Fatalf("stylesheet = %q", raw)
	}
}

// TestReadAssetRefusesWhatLeavesTheContentRoot is the page's side of the
// same rule the preflight applies to the manifest's own references: a
// path that stays inside the package, names a regular file, and is not a
// link out of it.
func TestReadAssetRefusesWhatLeavesTheContentRoot(t *testing.T) {
	store := installWithUI(t)
	content := filepath.Join(store.root, "hello", "content")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.js"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.js"),
		filepath.Join(content, "ui", "dist", "link.js")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(content, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(content, "ui", "dist", "index.css"), 0); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(content, "ui", "dist", "big.js")
	h, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Truncate(maxAssetBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		rel  string
		want string
	}{
		{"nothing", "", "asset path is required"},
		{"a walk out", "../layer.yaml", "must be a relative path inside the content root"},
		{"an absolute path", "/etc/passwd", "must be a relative path inside the content root"},
		{"a file that is not there", "ui/dist/gone.js", "is not there"},
		{"a directory", "ui/dist", "is a directory"},
		{"a symlink", "ui/dist/link.js", "is a symbolic link"},
		{"through a symlinked directory", "escape/secret.js", "resolves outside the content root"},
		{"a file over the limit", "ui/dist/big.js", "over the"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.ReadAsset("hello", tc.rel)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("read asset %q = %v, want %q", tc.rel, err, tc.want)
			}
		})
	}
}

func TestReadAssetRefusesAnApplicationThatIsNotInstalled(t *testing.T) {
	store := installWithUI(t)
	_, err := store.ReadAsset("gone", "ui/dist/index.js")
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("read asset of an uninstalled application = %v", err)
	}
}

// TestInspectReadsAnArchiveTheWayItReadsADirectory: the wizard's zip
// button hands an archive to the same call the folder button hands a
// directory, so the two have to answer the same card — and the same
// refusals, including the ones about where the files would land, which
// are measured against the extraction that stands in for the package.
func TestInspectReadsAnArchiveTheWayItReadsADirectory(t *testing.T) {
	src := newApp(t)
	store, root, dataDir := newStore(t)
	want, err := store.Inspect(context.Background(), src)
	if err != nil {
		t.Fatalf("inspect directory: %v", err)
	}
	for _, tc := range []struct {
		name   string
		prefix string
	}{
		{"the files at the archive root", ""},
		{"the files under one top-level directory", "hello-0.1.0/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.Inspect(
				context.Background(), zipTree(t, src, tc.prefix))
			if err != nil {
				t.Fatalf("inspect zip: %v", err)
			}
			if got.Summary != want.Summary || len(got.Layers) != len(want.Layers) ||
				len(got.Refusals) != 0 {
				t.Fatalf("inspection of the archive = %+v, want %+v",
					got, want)
			}
		})
	}
	// Reading an archive writes neither half of the registry: the
	// extraction lives in the system temp directory and is removed
	// before the call returns.
	mustBeEmpty(t, root)
	mustNotExist(t, filepath.Join(dataDir, "apps"))
}

// TestInspectRefusesAPathThePlatformWouldNotTake covers the archive half
// of the landing-path check: a zip is measured against the same content
// root a directory is, entry for entry, so a package the wizard refuses
// as a folder is refused as an archive too — and with the same row.
func TestInspectRefusesAPathThePlatformWouldNotTake(t *testing.T) {
	src := newApp(t)
	store, _, _ := newStore(t)
	// Short enough that the fixture's own files do not land inside it:
	// what is being tested is the measurement, not the platform.
	store.pathLimit = pathLength(store.landingRoot("hello")) + 4
	dir, err := store.Inspect(context.Background(), src)
	if err != nil {
		t.Fatalf("inspect directory: %v", err)
	}
	zip, err := store.Inspect(context.Background(), zipTree(t, src, ""))
	if err != nil {
		t.Fatalf("inspect zip: %v", err)
	}
	if len(dir.Refusals) != 1 || len(zip.Refusals) != 1 {
		t.Fatalf("refusals = %+v (directory), %+v (archive)",
			dir.Refusals, zip.Refusals)
	}
	if dir.Refusals[0] != zip.Refusals[0] {
		t.Errorf("the archive was refused %+v, the directory %+v",
			zip.Refusals[0], dir.Refusals[0])
	}
	row := dir.Refusals[0]
	if row.Key == "" || !strings.Contains(row.Reason, "would land on a") ||
		!strings.Contains(row.Reason, "file APIs accept") {
		t.Errorf("refusal = %+v", row)
	}
}

// TestInspectReportsAnArchiveOverTheBound: an archive that declares more
// than the host unpacks is an error rather than a row — nothing inside it
// could be read, so there is no card to report rows against — and the
// sentence is the one the person who packaged it needs: which entry, how
// big it declares itself, and the bound it broke.
func TestInspectReportsAnArchiveOverTheBound(t *testing.T) {
	store, root, _ := newStore(t)
	_, err := store.Inspect(context.Background(), zipWithBigEntry(t))
	var tooLarge *zipx.TooLarge
	if err == nil || !errors.As(err, &tooLarge) {
		t.Fatalf("inspect of an archive over the bound = %v", err)
	}
	if tooLarge.Entry != "big.bin" || tooLarge.Total {
		t.Errorf("refusal = %+v", tooLarge)
	}
	for _, want := range []string{`"big.bin"`, "64.0 MiB"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %s", err, want)
		}
	}
	mustBeEmpty(t, root)
}

// zipWithBigEntry writes an archive whose manifest is a valid package and
// whose one extra entry declares more than the extractor unpacks, which
// is what a stray dataset in the folder and a zip bomb both look like
// from here. The bytes are streamed, so the test does not hold 65 MiB.
func zipWithBigEntry(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	w, err := zw.Create(ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, fixtureManifest); err != nil {
		t.Fatal(err)
	}
	w, err = zw.Create("big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(w, &zeroReader{left: zipx.MaxFile + 1}); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// zeroReader reads n zero bytes without holding them.
type zeroReader struct{ left int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
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
