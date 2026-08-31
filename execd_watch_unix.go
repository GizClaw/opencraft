//go:build !windows

package main

import (
	"os"
	"time"
)

// watchParent invokes onDeath once ppid stops being the caller's
// parent. When a process dies, its children are reparented (to launchd
// or init), so a changed Getppid is a reliable death signal without
// relying on PID-reuse-prone kill probes.
func watchParent(ppid int, onDeath func()) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if os.Getppid() != ppid {
			onDeath()
			return
		}
	}
}
