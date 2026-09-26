package wirevocab

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestRetiredNamesAreWellFormed keeps the table itself honest: an entry
// with an empty replacement, a path that is not a repo path, or no
// reason would make the scan below unfalsifiable and un-reviewable.
func TestRetiredNamesAreWellFormed(t *testing.T) {
	seen := make(map[string]bool, len(Retired))
	for _, name := range Retired {
		switch {
		case name.Old == "":
			t.Error("an entry has no old name")
		case name.New == "":
			t.Errorf("%q: no replacement name", name.Old)
		case name.New == name.Old:
			t.Errorf("%q: replaced by itself", name.Old)
		case !strings.Contains(name.Owner, "/"):
			t.Errorf("%q: owner %q is not a package path", name.Old, name.Owner)
		case len(name.Why) < 40:
			t.Errorf("%q: the reason is too short to judge: %q",
				name.Old, name.Why)
		}
		if seen[name.Old] {
			t.Errorf("%q is listed twice", name.Old)
		}
		seen[name.Old] = true
	}
}

// TestRetiredNamesAreUnused is the scan the table exists for: no
// hand-written struct tag in this repo may use a retired field name.
//
// The tag is what counts, so a mention inside a comment does not trip it
// — the doc comments explain the rename and have to be able to say the
// old name. A file that legitimately has to read the old spelling (a
// compat reader for a document an older version wrote) does not get a
// quiet exemption: it adds a row to the table above, which is the edit a
// reviewer sees.
func TestRetiredNamesAreUnused(t *testing.T) {
	root := repoRoot(t)
	for _, name := range Retired {
		tag := regexp.MustCompile(`json:"` + regexp.QuoteMeta(name.Old) + `(,|")`)
		sites := scanJSONTags(t, root, tag)
		sort.Strings(sites)
		for _, site := range sites {
			t.Errorf("%s: a struct tag still uses the retired field "+
				"name %q: use %q (%s, owned by %s)",
				site, name.Old, name.New, name.Why, name.Owner)
		}
	}
}

// scanJSONTags returns the slash paths (with line numbers) of every
// production Go file under root whose code matches tag.
//
// Exemptions live here, once, with the reason:
//   - `_test.go` — a test may pin the old spelling on purpose (a bundle
//     an older version wrote still loads, the field is gone from the
//     answer);
//   - `*.pb.go` — generated protobuf, per the package comment.
//
// The scan reads lines, not an AST: everything from the first `//` on a
// line is dropped (which is how a doc comment may name the old
// spelling), so a `//` inside a string literal ends the considered line
// too and a tag behind one is invisible here — another reason to keep
// one field per line.
func scanJSONTags(t *testing.T, root string, tag *regexp.Regexp) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			switch entry.Name() {
			case ".git", ".opencraft", ".task", "node_modules":
				return fs.SkipDir
			}
			// Generated or vendored trees, and the JS plugins at the
			// repo root (internal/capabilities/plugins is production
			// Go and stays in scope).
			switch filepath.ToSlash(rel) {
			case "frontend", "build", "dist", "Casks", "plugins", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case !strings.HasSuffix(entry.Name(), ".go"),
			strings.HasSuffix(entry.Name(), "_test.go"),
			strings.HasSuffix(entry.Name(), ".pb.go"):
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		slashRel := filepath.ToSlash(rel)
		for i, line := range strings.Split(string(raw), "\n") {
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
			if tag.MatchString(line) {
				out = append(out, fmt.Sprintf("%s:%d", slashRel, i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// repoRoot walks up from this file until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above this test file")
		}
		dir = parent
	}
}
