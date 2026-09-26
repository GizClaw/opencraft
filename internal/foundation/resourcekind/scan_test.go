package resourcekind

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

// declaration is one `const <name> = "<value>"` found in the tree.
type declaration struct {
	name  string
	value string
	file  string
	line  int
}

// TestDeclaredKindConstantsAreInventoried is the scan the frozen list
// exists for: every `const xResourceKind = "..."` in the repo must be
// listed in Kinds, and every entry in Kinds must still be declared. It
// fails in both directions on purpose — a new kind has to be added here
// (an edit a reviewer sees), and a renamed or deleted one cannot leave
// a stale entry behind.
func TestDeclaredKindConstantsAreInventoried(t *testing.T) {
	declared := scanConstDeclarations(t, kindSuffixes)
	inventory := make(map[string]Kind, len(Kinds))
	for _, kind := range Kinds {
		inventory[kind.Value] = kind
	}

	for value, decls := range declared {
		if _, ok := inventory[value]; !ok {
			t.Errorf("kind %q (%s) is not in Kinds: add an entry with "+
				"an owner and a note", value, describe(decls))
		}
	}
	for _, kind := range Kinds {
		if _, ok := declared[kind.Value]; !ok {
			t.Errorf("Kinds lists %q (owner %s) but nothing declares it: "+
				"change the entry to the new value or drop it",
				kind.Value, kind.Owner)
		}
	}
	// A value declared in two packages is a merge conflict waiting to
	// happen: the two copies drift apart and the registry answers
	// whichever registered last.
	for value, decls := range declared {
		if len(decls) > 1 && inventory[value].Owner != "" {
			t.Errorf("kind %q is declared in several places: %s",
				value, describe(decls))
		}
	}
}

// TestDeclaredImplConstantsAreInventoried does the same for impl
// strings. They have no spelling rule (they are labels inside one kind),
// but a typo in one is just as silent.
func TestDeclaredImplConstantsAreInventoried(t *testing.T) {
	declared := scanConstDeclarations(t, []string{"ResourceImpl"})
	known := make(map[string]bool, len(Impls))
	for _, impl := range Impls {
		known[impl] = true
	}
	for value, decls := range declared {
		if !known[value] {
			t.Errorf("impl %q (%s) is not in Impls", value, describe(decls))
		}
	}
	for _, impl := range Impls {
		if _, ok := declared[impl]; !ok {
			t.Errorf("Impls lists %q but nothing declares it", impl)
		}
	}
}

// TestAssetKindsAreInventoriedOrFlowcraft keeps the deploy surface and
// the inventory in step: the embedded documents are what a user's layer
// is merged on top of, so a kind that appears there (and nowhere in Go)
// is either flowcraft's, spelled per the rule, or a typo.
func TestAssetKindsAreInventoriedOrFlowcraft(t *testing.T) {
	root := repoRoot(t)
	assets := filepath.Join("internal", "foundation", "config", "assets")
	entries, err := os.ReadDir(filepath.Join(root, assets))
	if err != nil {
		t.Fatal(err)
	}
	inventory := make(map[string]bool, len(Kinds))
	for _, kind := range Kinds {
		inventory[kind.Value] = true
	}
	found := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(root, assets, entry.Name())
		for i, line := range readLines(t, path) {
			match := assetKindPattern.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			value := match[1]
			found++
			if inventory[value] || Matches(value) {
				continue
			}
			t.Errorf("%s:%d: kind %q is neither inventoried nor "+
				"spelled per the rule", filepath.Join(assets,
				entry.Name()), i+1, value)
		}
	}
	if found == 0 {
		t.Fatal("no kind: entries found in the embedded assets")
	}
}

var (
	// A plain `const X = "v"` or a line inside a const block.
	constPattern = regexp.MustCompile(`^(?:const\s+)?(\w+)\s*=\s*"([^"]*)"\s*$`)
	// A `kind: <value>` entry in an embedded deploy document.
	assetKindPattern = regexp.MustCompile(`^\s*kind:\s*(\S+)\s*$`)
	kindSuffixes     = []string{"ResourceKind"}
)

// scanConstDeclarations returns every constant whose name ends in one of
// the suffixes, grouped by value.
//
// Exemptions, both documented so the next reader does not have to guess:
//   - `_test.go` files: a test may declare a kind to prove the registry
//     rejects an unknown one, and those values are deliberately not real.
//   - `internal/foundation/compat`: the migration steps keep old spellings
//     as literals, which is exactly what makes them safe to delete once
//     the migration window closes; inventorying them would freeze them.
func scanConstDeclarations(t *testing.T, suffixes []string) map[string][]declaration {
	t.Helper()
	root := repoRoot(t)
	out := make(map[string][]declaration)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".opencraft", ".task", "frontend", "node_modules",
				"build", "dist":
				return fs.SkipDir
			}
			if filepath.ToSlash(rel) == "internal/foundation/compat" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		for i, line := range readLines(t, path) {
			match := constPattern.FindStringSubmatch(strings.TrimSpace(line))
			if match == nil {
				continue
			}
			name, value := match[1], match[2]
			if !hasSuffix(name, suffixes) {
				continue
			}
			out[value] = append(out[value], declaration{
				name: name, value: value,
				file: filepath.ToSlash(rel), line: i + 1,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("the const scan found nothing: check the pattern")
	}
	return out
}

func hasSuffix(name string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func describe(decls []declaration) string {
	parts := make([]string, 0, len(decls))
	for _, decl := range decls {
		parts = append(parts, fmt.Sprintf("%s:%d declares %s",
			decl.file, decl.line, decl.name))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(raw), "\n")
}

// repoRoot walks up from this file until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", file)
		}
		dir = parent
	}
}
