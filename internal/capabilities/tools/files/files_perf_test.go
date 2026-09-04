package files

import (
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/testing/perf"
)

func benchData() []byte {
	var b strings.Builder
	for i := 0; i < 20_000; i++ {
		b.WriteString("line with some content ")
		b.WriteString(strings.Repeat("x", 80))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func BenchmarkJoinLineRange(b *testing.B) {
	data := benchData()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = joinLineRange(data, 100, 200)
	}
}

// TestJoinLineRangeWithinBudget guards the line-slicing path: reading a
// narrow range of a 2 MiB file must not regress into splitting the
// whole file.
func TestJoinLineRangeWithinBudget(t *testing.T) {
	data := benchData()
	perf.AssertMedianWithin(t, 10, func() {
		_ = joinLineRange(data, 100, 200)
	}, 25*time.Millisecond)
}
