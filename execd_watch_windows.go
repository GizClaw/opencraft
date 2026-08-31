//go:build windows

package main

import (
	"time"

	xwin "golang.org/x/sys/windows"
)

// watchParent invokes onDeath once the parent process exits. Windows
// never reparents orphans (Getppid stays fixed after the parent dies),
// so the watchdog waits on a handle to the parent process instead: the
// process object is signalled when the parent terminates, which is the
// Windows equivalent of the unix reparenting signal. The wait is polled
// so a transient handle error cannot wedge the child.
func watchParent(ppid int, onDeath func()) {
	handle, err := xwin.OpenProcess(
		xwin.SYNCHRONIZE|xwin.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(ppid),
	)
	if err != nil {
		// Parent already gone (or not observable): the watchdog cannot
		// keep watching, so treat it as parent death.
		onDeath()
		return
	}
	defer xwin.CloseHandle(handle)

	const poll = 500 * time.Millisecond
	for {
		ev, err := xwin.WaitForSingleObject(handle, uint32(poll/time.Millisecond))
		if err != nil || ev == xwin.WAIT_OBJECT_0 {
			onDeath()
			return
		}
	}
}
