package desktop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/perf"
)

func benchWorkspace(tb testing.TB) string {
	tb.Helper()
	wd := tb.TempDir()
	for i := 0; i < 1000; i++ {
		dir := filepath.Join(wd, "src", "pkg", string(rune('a'+i%26)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(dir, "file_"+string(rune('a'+i%26))+".txt"),
			[]byte("x"), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return wd
}

func BenchmarkManifestSnapshot(b *testing.B) {
	wd := benchWorkspace(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manifestSnapshot(context.Background(), wd)
	}
}

func TestManifestSnapshotWithinBudget(t *testing.T) {
	wd := benchWorkspace(t)
	perf.AssertMedianWithin(t, 10, func() {
		if _, err := manifestSnapshot(context.Background(), wd); err != nil {
			t.Fatal(err)
		}
	}, 250*time.Millisecond)
}
