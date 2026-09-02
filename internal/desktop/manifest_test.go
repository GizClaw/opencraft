package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestSnapshotStopsAtCap(t *testing.T) {
	old := maxManifestEntries
	maxManifestEntries = 10
	t.Cleanup(func() { maxManifestEntries = old })

	wd := t.TempDir()
	for i := 0; i < 50; i++ {
		if err := os.WriteFile(
			filepath.Join(wd, "f"+string(rune('a'+i%26))+".txt"),
			[]byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := manifestSnapshot(context.Background(), wd)
	if !errors.Is(err, errManifestTooLarge) {
		t.Fatalf("manifestSnapshot error = %v, want errManifestTooLarge", err)
	}
}
