package worldstate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeGitOnPath installs a `git` that always prints n bytes of output,
// so gitBounded can be exercised without a real repository.
func fakeGitOnPath(t testing.TB, bytes int) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nhead -c " + fmt.Sprint(bytes) + " /dev/zero | tr '\\0' x\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGitBoundedKillsOversizedOutput(t *testing.T) {
	fakeGitOnPath(t, 128<<10)
	out, truncated := gitBoundedWithTimeout(
		context.Background(), t.TempDir(), 4096, time.Minute, "status")
	if !truncated {
		t.Fatal("oversized git output must be reported truncated")
	}
	if out != "" {
		t.Fatalf("truncated git output must be discarded, got %q", out)
	}
}

func BenchmarkGitBoundedSmall(b *testing.B) {
	fakeGitOnPath(b, 1024)
	root := b.TempDir()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = gitBounded(context.Background(), root, 4096, "status")
	}
}
