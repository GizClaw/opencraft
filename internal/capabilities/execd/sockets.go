package execd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

const (
	// socketNamePrefix and socketNameSuffix bracket the socket names this
	// package creates. The random part in between keeps two children from
	// colliding, which also means a leaked name never blocks a later
	// launch: leftovers only accumulate.
	socketNamePrefix = "execd-"
	socketNameSuffix = ".sock"

	// staleSocketAge is how long a socket has to sit untouched before a
	// sweep may remove it. A child removes its own socket when it stops,
	// and its watchdog takes that path when the parent died — so a file
	// still sitting there a day later belongs to a host that was killed
	// before any of those paths could run, and is never coming back.
	staleSocketAge = 24 * time.Hour
)

// socketSweepOnce keeps the sweep to one per process.
var socketSweepOnce sync.Once

// sweepStaleSocketsOnce removes sockets leaked by earlier runs. The
// launch path calls it, so a host cleans up after its own crashes the
// first time it needs a child (a second after startup in practice)
// without a scheduler of its own.
func sweepStaleSocketsOnce(ctx context.Context) {
	socketSweepOnce.Do(func() {
		dir, err := socketDir()
		if err != nil {
			return
		}
		if removed := sweepStaleSockets(dir, time.Now()); removed > 0 {
			telemetry.Info(ctx, "execd: removed stale sockets",
				otellog.Int("count", removed))
		}
	})
}

// sweepStaleSockets removes the execd sockets in dir that are older
// than staleSocketAge and reports how many it removed. Only this
// package's own naming pattern is touched, and only by age: the socket
// of a live child is at most as old as the runtime that owns it, while
// the cutoff is a day.
func sweepStaleSockets(dir string, now time.Time) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	cutoff := now.Add(-staleSocketAge)
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() ||
			!strings.HasPrefix(name, socketNamePrefix) ||
			!strings.HasSuffix(name, socketNameSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			removed++
		}
	}
	return removed
}
