package apps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/platform/maxpath"
)

// Where a package's files would land is a question only the platform can
// answer, and it answers it differently by platform: Windows fails a path
// past MAX_PATH with an error that says nothing about the length, POSIX
// caps one name at 255 bytes with an error that reads as itself. These
// tests pin both halves of that rule — the rule, and what the host does
// with it — on whatever platform they run on.

// TestStoreAsksTheHostForItsPathLimit pins the wiring: a registry takes
// the cap from the host it runs on (platform/maxpath owns the rule and
// its own test), which is what makes this check live on the Windows lane
// and cost nothing elsewhere. It fails on the platform whose answer is
// not zero; every test below sets the field itself instead.
func TestStoreAsksTheHostForItsPathLimit(t *testing.T) {
	store, _, _ := newStore(t)
	if got, want := store.pathLimit, maxpath.HostLimit(); got != want {
		t.Errorf("store path limit = %d, want the host's %d", got, want)
	}
}

// TestPathLengthCountsAPathTheWayWindowsDoes: the limit is in UTF-16 code
// units, so a package whose directories are named in Chinese is not
// counted three bytes per character — Windows takes that path, and a byte
// count would refuse it.
func TestPathLengthCountsAPathTheWayWindowsDoes(t *testing.T) {
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/a/b", 4},
		{"/你好/b", 5}, // 9 bytes, 5 code units
		{"", 0},
	} {
		if got := pathLength(tc.path); got != tc.want {
			t.Errorf("pathLength(%q) = %d, want %d", tc.path, got, tc.want)
		}
	}
}

// TestFirstLongLandingPathWalksWhatTheCopyCopies: the check answers about
// the files an install would land, not about everything the tree holds —
// a .git directory full of long names is not copied, a link is refused by
// the copy on its own account, so neither is a path this check reports.
func TestFirstLongLandingPathWalksWhatTheCopyCopies(t *testing.T) {
	tree := t.TempDir()
	writeTestFile(t, tree, "shallow.txt", "x")
	writeTestFile(t, tree, "deep/deeper/deepest/buried.txt", "x")
	writeTestFile(t, tree, ".git/objects/"+strings.Repeat("c", 40), "x")
	if err := os.Symlink(
		filepath.Join(tree, "shallow.txt"),
		filepath.Join(tree, strings.Repeat("d", 40)),
	); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join("/apps", "hello", "content")

	// No limit is no check.
	if got, err := firstLongLandingPath(tree, dest, 0); err != nil || got != nil {
		t.Fatalf("checked with no limit: %+v, %v", got, err)
	}

	// A limit nothing breaks finds nothing: the deep file fits exactly.
	deepest := pathLength(filepath.Join(dest, "deep/deeper/deepest/buried.txt"))
	if got, err := firstLongLandingPath(tree, dest, deepest); err != nil || got != nil {
		t.Fatalf("limit %d found %+v (%v), want nothing", deepest, got, err)
	}

	// One character less and the deepest file is the one reported: the
	// walk descends, and the path it reports is the landing path.
	got, err := firstLongLandingPath(tree, dest, deepest-1)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Entry != "deep/deeper/deepest/buried.txt" {
		t.Fatalf("limit %d found %+v, want the deepest file", deepest-1, got)
	}
	if want := filepath.Join(dest, "deep/deeper/deepest/buried.txt"); got.Landing != want {
		t.Errorf("landing = %q, want %q", got.Landing, want)
	}

	// A limit so small that everything is too long reports the first
	// entry the copy would land, which the walk reaches after skipping
	// two longer ones: .git sorts ahead of it, the link ahead of that,
	// and neither is a path the copy lands.
	got, err = firstLongLandingPath(tree, dest, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Entry != "deep" {
		t.Fatalf("limit 1 found %+v, want the deep directory", got)
	}
}

// TestThePreflightAndTheInstallMeasureTheSamePaths is the property the
// two halves exist for: a package the wizard called installable is one
// the install takes, because both measure the landing paths against the
// same content root. The boundary is the point of it — a path of exactly
// the limit fits, one character more does not.
func TestThePreflightAndTheInstallMeasureTheSamePaths(t *testing.T) {
	src := newApp(t)
	store, root, _ := newStore(t)
	// nodes/hello.js is the longest path in the fixture.
	longest := pathLength(filepath.Join(store.landingRoot("hello"), "nodes/hello.js"))
	store.pathLimit = longest

	insp, err := store.Inspect(context.Background(), src)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(insp.Refusals) != 0 {
		t.Fatalf("refusals at the limit = %+v, want none", insp.Refusals)
	}
	install(t, store, src)
	mustExist(t, filepath.Join(root, "hello", "content", "nodes/hello.js"))

	// One character of room less and neither half takes it any more.
	store.pathLimit = longest - 1
	insp, err = store.Inspect(context.Background(), src)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(insp.Refusals) != 1 ||
		insp.Refusals[0].Key != "nodes/hello.js" {
		t.Fatalf("refusals one short = %+v, want one about nodes/hello.js",
			insp.Refusals)
	}
	_, err = store.Install(context.Background(), src, InstallOptions{ID: "again"})
	var tooLong *PathTooLong
	if !errors.As(err, &tooLong) {
		t.Fatalf("install = %v, want the path refused", err)
	}
	if tooLong.Entry != "nodes/hello.js" || tooLong.Limit != longest-1 {
		t.Errorf("install refusal = %+v", tooLong)
	}
	// The same sentence the row carried, and nothing written: the check
	// runs before the copy, not partway through it.
	if want := insp.Refusals[0].Reason; tooLong.Error() != want {
		t.Errorf("install refusal says %q, the row said %q", tooLong.Error(), want)
	}
	mustNotExist(t, filepath.Join(root, "again"))
}

// TestUpdateMeasuresTheNewPackagesPathsToo: an update lands its files
// where the installed version is, so it asks the same question — and a
// package that cannot land there leaves the installed version serving.
func TestUpdateMeasuresTheNewPackagesPathsToo(t *testing.T) {
	store, _, _ := newStore(t)
	install(t, store, newApp(t))
	next := appAt(t, "0.2.0")
	store.pathLimit = pathLength(store.landingRoot("hello")) + 4
	_, err := store.Update(context.Background(), "hello", next)
	var tooLong *PathTooLong
	if !errors.As(err, &tooLong) {
		t.Fatalf("update = %v, want the path refused", err)
	}
	if sum, err := store.Get("hello"); err != nil || sum.Version != "0.1.0" {
		t.Fatalf("installed version after the refusal = %+v (%v)", sum, err)
	}
}
