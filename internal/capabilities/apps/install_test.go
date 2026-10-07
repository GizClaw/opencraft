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
)

// Installing is the one write the registry does: a package the preflight
// accepted is staged inside the registry, validated there, and renamed
// into place. What these tests are about is the boundary around it — the
// wizard's edits land in the copy and never in the user's directory, an
// edit the manifest rules refuse fails the install instead of landing,
// and an archive installs the same package a directory does.

// ptr returns a pointer to one install-form value, which is how a caller
// says "the user edited this field".
func ptr(value string) *string { return &value }

// zipTree packs one directory the way a release artifact or an exported
// folder looks: every file under prefix, which may be empty (the
// package's files then sit at the archive root).
func zipTree(t *testing.T, src, prefix string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	err = filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(prefix + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInstallWritesTheFormsEditsIntoTheCopy pins the whole point of the
// form: the id a user typed is the id the application installs under,
// and the package they picked is left exactly as it was.
func TestInstallWritesTheFormsEditsIntoTheCopy(t *testing.T) {
	src := newApp(t)
	before := snapshotTree(t, src)
	store, root, _ := newStore(t)
	sum, err := store.Install(context.Background(), src, InstallOptions{
		ID:   "renamed",
		Name: "Renamed",
		Icon: ptr("R"),
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if sum.ID != "renamed" || sum.Name != "Renamed" || sum.Icon != "R" {
		t.Fatalf("summary = %+v", sum)
	}
	app, err := store.Get("renamed")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if app.ContentDir != filepath.Join(root, "renamed", "content") {
		t.Fatalf("content dir = %q", app.ContentDir)
	}
	m, err := store.Manifest("renamed")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.ID != "renamed" || m.Name != "Renamed" || m.Icon != "R" {
		t.Fatalf("installed manifest = %+v", m)
	}
	if m.Version != "0.1.0" || len(m.Layers) != 1 || m.Layers[0] != "layer.yaml" {
		t.Fatalf("the edits replaced the rest of the manifest: %+v", m)
	}
	if _, err := store.Get("hello"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("the source id installed too: %v", err)
	}
	mustBeUntouched(t, src, before)
	mustNotExist(t, filepath.Join(root, "hello"))
}

// TestInstallRemovesAnIconTheFormCleared is the other side of the pointer:
// a form that emptied the icon field means "no icon", not "leave it".
func TestInstallRemovesAnIconTheFormCleared(t *testing.T) {
	src := newApp(t)
	writeTestFile(t, src, ManifestFile, fixtureManifest+"icon: icon.png\n")
	writeTestFile(t, src, "icon.png", "not really a png, but a file")
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), src, InstallOptions{
		Icon: ptr(""),
	}); err != nil {
		t.Fatalf("install: %v", err)
	}
	m, err := store.Manifest("hello")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.Icon != "" {
		t.Fatalf("icon = %q, want it cleared", m.Icon)
	}
}

// TestInstallKeepsWhatTheFormDidNotEdit pins the rest of the zero value:
// a caller that edits only the id may not lose the icon, and may not
// dereference the pointer it did not set.
func TestInstallKeepsWhatTheFormDidNotEdit(t *testing.T) {
	src := newApp(t)
	writeTestFile(t, src, ManifestFile, fixtureManifest+"icon: icon.png\n")
	writeTestFile(t, src, "icon.png", "not really a png, but a file")
	store, _, _ := newStore(t)
	if _, err := store.Install(context.Background(), src, InstallOptions{
		ID: "renamed",
	}); err != nil {
		t.Fatalf("install: %v", err)
	}
	m, err := store.Manifest("renamed")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.Icon != "icon.png" {
		t.Fatalf("icon = %q, want the manifest's own", m.Icon)
	}
	if m.Name != "Hello" {
		t.Fatalf("name = %q, want the manifest's own", m.Name)
	}
}

// TestInstallFromTheFormsStartingPointChangesNothing closes the wizard's
// loop: the form starts from InstallOptionsFromSummary, and a user who
// edits nothing installs the package as written — the same bytes, not a
// manifest the host rewrote on its way in.
func TestInstallFromTheFormsStartingPointChangesNothing(t *testing.T) {
	src := newAppWithUI(t)
	manifest := "# written by hand\n" + fixtureManifest +
		"icon: icon.png\nui:\n  entry: ui/dist/index.js\n"
	writeTestFile(t, src, ManifestFile, manifest)
	store, root, _ := newStore(t)
	insp, err := store.Inspect(context.Background(), src)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if _, err := store.Install(
		context.Background(), src, InstallOptionsFromSummary(insp.Summary),
	); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "hello", "content", ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != manifest {
		t.Fatalf("installed manifest =\n%s\nwant the source verbatim:\n%s", got, manifest)
	}
}

// TestInstallRefusesAnEditTheManifestRulesRefuse pins that the form's
// edits are validated exactly like an author's: an id the host cannot
// spell fails the install, and the failure lands before anything is
// copied.
func TestInstallRefusesAnEditTheManifestRulesRefuse(t *testing.T) {
	src := newApp(t)
	before := snapshotTree(t, src)
	store, root, _ := newStore(t)
	_, err := store.Install(context.Background(), src, InstallOptions{ID: "Hello World"})
	if err == nil || !strings.Contains(err.Error(), `id "Hello World" is not an application id`) {
		t.Fatalf("install with an unspellable id = %v", err)
	}
	var refusals Refusals
	if errors.As(err, &refusals) {
		t.Fatalf("a manifest the form produced is refused as a document, not as rows: %v", err)
	}
	mustBeEmpty(t, root)
	mustBeUntouched(t, src, before)

	// An icon the form typed has to be in the package like one an author
	// declared: the staged copy is what the preflight reads.
	_, err = store.Install(context.Background(), src, InstallOptions{Icon: ptr("brand/new.png")})
	var rows Refusals
	if !errors.As(err, &rows) {
		t.Fatalf("install with a missing icon = %v, want a refusal", err)
	}
	refusedEvery(t, rows, "brand/new.png: the icon does not exist inside the content root")
	mustBeEmpty(t, root)
	mustBeUntouched(t, src, before)
}

// TestInstallCopiesAnUneditedManifestByteForByte pins that the rewrite
// only happens when there is something to rewrite: an install as written
// keeps the author's comments and formatting, so a package that edits
// itself never passes through this host twice.
func TestInstallCopiesAnUneditedManifestByteForByte(t *testing.T) {
	src := newApp(t)
	manifest := "# written by hand\n" + fixtureManifest + "description: 'a fixture # not a comment'\n"
	writeTestFile(t, src, ManifestFile, manifest)
	store, root, _ := newStore(t)
	install(t, store, src)
	raw, err := os.ReadFile(filepath.Join(root, "hello", "content", ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != manifest {
		t.Fatalf("installed manifest =\n%s\nwant the source verbatim:\n%s", raw, manifest)
	}
}

func TestInstallZipInstallsThePackageTheArchiveCarried(t *testing.T) {
	zipPath := zipTree(t, newApp(t), "hello-0.1.0/")
	store, root, _ := newStore(t)
	sum, err := store.InstallZip(context.Background(), zipPath, InstallOptions{ID: "zipped"})
	if err != nil {
		t.Fatalf("install zip: %v", err)
	}
	if sum.ID != "zipped" || !sum.Enabled {
		t.Fatalf("summary = %+v", sum)
	}
	app, err := store.Get("zipped")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if app.ContentDir != filepath.Join(root, "zipped", "content") {
		t.Fatalf("content dir = %q", app.ContentDir)
	}
	mustExist(t, filepath.Join(app.ContentDir, "nodes", "hello.js"))
}

func TestInstallZipRefusesAnArchiveWithoutAManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("hello/layer.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "version: v1\n"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	store, root, _ := newStore(t)
	if _, err := store.InstallZip(context.Background(), path, InstallOptions{}); err == nil ||
		!strings.Contains(err.Error(), "zip has no "+ManifestFile) {
		t.Fatalf("install zip without a manifest = %v", err)
	}
	mustBeEmpty(t, root)
}

// TestInstallRefusesASourceInsideTheRegistry pins the guard against
// installing a package out of the registry into itself: the copy would
// recurse into the tree it is copying.
func TestInstallRefusesASourceInsideTheRegistry(t *testing.T) {
	store, root, _ := newStore(t)
	install(t, store, newApp(t))
	_, err := store.Install(
		context.Background(),
		filepath.Join(root, "hello", "content"),
		InstallOptions{ID: "again"})
	if err == nil || !strings.Contains(err.Error(), "is inside the application root") {
		t.Fatalf("install out of the registry = %v", err)
	}
}
