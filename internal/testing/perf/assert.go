// Package perf provides small helpers for performance guard tests:
// absolute-threshold assertions that fail CI when a hot path regresses
// beyond a generous bound. Timing assertions are skipped under
// `go test -short` and the race detector, which distort timings.
package perf

import (
	"sort"
	"testing"
	"time"
)

// SkipTiming skips timing-sensitive assertions under -short and -race.
func SkipTiming(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("perf timing assertion skipped under -short")
	}
	if raceEnabled() {
		t.Skip("perf timing assertion skipped under -race")
	}
}

func raceEnabled() bool {
	// The race detector sets this at build time; testing.Short covers
	// most CI paths, and this guards local -race runs.
	return raceFlag
}

var raceFlag = false

// AssertMedianWithin runs fn n times and fails when the median duration
// exceeds limit. Use a generous limit: this is a regression tripwire,
// not a precise benchmark.
func AssertMedianWithin(t testing.TB, n int, fn func(), limit time.Duration) {
	t.Helper()
	SkipTiming(t)
	if n <= 0 {
		n = 10
	}
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		fn()
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})
	median := durations[len(durations)/2]
	if median > limit {
		t.Fatalf("median duration = %v, want <= %v", median, limit)
	}
}
