//go:build !windows

package execd

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

// configureChildProcess puts the child in its own process group so the
// parent can signal the whole group, and the orphan sweep can do the
// same for a leftover child.
func configureChildProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// processAlive reports whether pid names a live process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// isExecdChild verifies that the recorded pid still is one of our
// children before the sweep kills it. The nonce is random per launch,
// so a reused pid does not match.
func isExecdChild(record childRecord) bool {
	cmdline := readCommandLine(record.PID)
	if cmdline == "" {
		return false
	}
	if record.Nonce != "" {
		return strings.Contains(cmdline, record.Nonce)
	}
	return strings.Contains(cmdline, "execd")
}

func readCommandLine(pid int) string {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// killTree SIGKILLs pid and every descendant, deepest first. The child
// is a process-group leader, so the group signal covers the common
// case; the descendant walk covers sandbox sessions that moved into
// their own groups.
func killTree(pid int) {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil &&
		err != syscall.ESRCH {
		telemetry.WarnErr(context.Background(),
			"execd: kill process group failed", err)
	}
	out, err := exec.Command("ps", "-ax", "-o", "pid=,ppid=").Output()
	if err != nil {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil &&
			err != syscall.ESRCH {
			telemetry.WarnErr(context.Background(),
				"execd: kill process failed", err)
		}
		return
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		child, err1 := strconv.Atoi(fields[0])
		parent, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[parent] = append(children[parent], child)
	}
	var walk func(int)
	walk = func(current int) {
		for _, child := range children[current] {
			walk(child)
		}
		if err := syscall.Kill(current, syscall.SIGKILL); err != nil &&
			err != syscall.ESRCH {
			telemetry.WarnErr(context.Background(),
				"execd: kill process in tree failed", err)
		}
	}
	walk(pid)
}
