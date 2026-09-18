package fshidden

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// entriesByName reads dir into a name-keyed map, so the platform test
// files can assert about one entry without depending on sort order.
func entriesByName(t *testing.T, dir string) map[string]fs.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]fs.DirEntry, len(entries))
	for _, entry := range entries {
		out[entry.Name()] = entry
	}
	return out
}

// writeEntries creates one plain file per name and fails the test when
// the fixture cannot be laid out.
func writeEntries(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(
			filepath.Join(dir, name), []byte("x"), 0o644,
		); err != nil {
			t.Fatal(err)
		}
	}
}
