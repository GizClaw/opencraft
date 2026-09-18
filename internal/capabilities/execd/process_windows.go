//go:build windows

package execd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// configureChildProcess is a no-op on Windows: job objects own the
// process tree here.
func configureChildProcess(*exec.Cmd) {}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// isExecdChild verifies that the recorded pid still is our exec child:
// the image has to be this executable and, because the journal records
// it, the process has to have started when the record says. A pid
// Windows reused for an unrelated process fails both checks, and an
// unverifiable process is left alone instead of killed.
func isExecdChild(record childRecord) bool {
	if record.PID <= 0 || record.CreatedAt <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(record.PID))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	self, err := os.Executable()
	if err != nil {
		return false
	}
	image := make([]uint16, 4096)
	size := uint32(len(image))
	if err := windows.QueryFullProcessImageName(
		handle, 0, &image[0], &size); err != nil {
		return false
	}
	if !strings.EqualFold(
		filepath.Base(windows.UTF16ToString(image[:size])),
		filepath.Base(self),
	) {
		return false
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(
		handle, &created, &exited, &kernel, &user); err != nil {
		return false
	}
	// CreatedAt is stamped immediately before the fork, so allow a
	// little slack for clock granularity.
	started := time.Unix(0, created.Nanoseconds())
	return !started.Before(
		time.UnixMilli(record.CreatedAt).Add(-3 * time.Second))
}

func killTree(pid int) {
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	_ = cmd.Run()
}
