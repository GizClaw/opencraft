//go:build linux

package procmem

import (
	"os"
	"strconv"
)

// procRoot is the /proc mount the probe reads. Linux desktop builds read
// the host's; a container that mounts /proc elsewhere can point it at that
// mount.
var procRoot = "/proc"

// Snapshot reads the family of the current process. Linux spawns the web
// helpers as ordinary children, so the process tree already names them and
// no ownership API is needed.
func Snapshot() (Family, error) {
	root := os.Getpid()
	family := assemble(root, readProcFS(procRoot), nil)
	for i := range family.Members {
		member := &family.Members[i]
		dir := strconv.Itoa(member.PID)
		if footprint, resident, ok := procMemory(procRoot, dir); ok {
			member.Footprint, member.Resident = footprint, resident
		}
		member.Path = execPath(procRoot, dir)
	}
	return family, nil
}
