package summarytext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFrontendSummaryMarkerMatches keeps the one marker literal honest.
//
// A compaction summary is a user message whose text starts with
// SummaryPrefix. Go writes it (the fold tool) and filters it back out of
// the persisted conversation (the memory hooks), and the chat has to
// recognize it to render a compact card instead of a user bubble. The
// frontend cannot import Go, so it carries its own copy
// (frontend/src/lib/compact.ts) — this test is what stops the two from
// drifting.
//
// It used to be pinned through the cross-language estimate fixture; that
// fixture is gone (the compaction node now calls EstimateTokens instead
// of mirroring it), so the marker is pinned here directly.
func TestFrontendSummaryMarkerMatches(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "frontend", "src", "lib", "compact.ts"))
	if err != nil {
		t.Fatal(err)
	}
	const marker = "COMPACT_SUMMARY_PREFIX ="
	ts := string(raw)
	at := strings.Index(ts, marker)
	if at < 0 {
		t.Fatalf("frontend/src/lib/compact.ts no longer declares %q", marker)
	}
	rest := ts[at+len(marker):]
	// The declaration is `COMPACT_SUMMARY_PREFIX =\n  '…';` — the value
	// may sit on the next line, and either quote style is fine.
	quote := strings.IndexAny(rest, `'"`)
	if quote < 0 {
		t.Fatal("the marker declaration carries no string literal")
	}
	body := rest[quote+1:]
	end := strings.IndexByte(body, rest[quote])
	if end < 0 {
		t.Fatal("the marker literal is unterminated")
	}
	if got, want := body[:end], SummaryPrefix; got != want {
		t.Fatalf("frontend marker = %q, want %q", got, want)
	}
}

// repoRoot walks up from the package directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
