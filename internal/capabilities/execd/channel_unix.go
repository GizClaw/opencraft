//go:build !windows

package execd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// prepareChannel creates an unnamed socketpair and arranges for the
// child end to be inherited as fd 3. No filesystem path, permission
// mode, or cleanup is involved, and a parent that dies closes its end,
// which the child sees as EOF.
func prepareChannel() (*channelPlan, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("execd: socketpair: %w", err)
	}
	unix.CloseOnExec(fds[0])
	unix.CloseOnExec(fds[1])
	parent := os.NewFile(uintptr(fds[0]), "execd-parent")
	child := os.NewFile(uintptr(fds[1]), "execd-child")
	if parent == nil || child == nil {
		closeLog(context.Background(), "execd: close socketpair after wrap failure",
			os.NewFile(uintptr(fds[0]), "execd-parent-drop"))
		return nil, errors.New("execd: socketpair file wrap failed")
	}
	plan := &channelPlan{
		args:       []string{"-execd-fd", "3"},
		extraFiles: []*os.File{child},
	}
	plan.attach = func(context.Context) (net.Conn, error) {
		conn, err := net.FileConn(parent)
		// FileConn dups the descriptor; the original copy must be
		// closed or the child would never see EOF when we close the
		// connection.
		if err != nil {
			closeLog(context.Background(), "execd: close parent channel end failed",
				parent)
			return nil, err
		}
		if err := parent.Close(); err != nil {
			closeLog(context.Background(), "execd: close channel after dup failed",
				conn)
			return nil, err
		}
		return conn, nil
	}
	plan.close = func() {
		closeLog(context.Background(), "execd: close parent channel end failed",
			parent)
		closeLog(context.Background(), "execd: close child channel end failed",
			child)
	}
	return plan, nil
}

// childChannel wraps the inherited socketpair end. It only runs in the
// execd child process.
func ChildChannel(fd int, _ string) (net.Conn, error) {
	if fd <= 0 {
		return nil, errors.New("execd: -execd-fd is required")
	}
	file := os.NewFile(uintptr(fd), "execd-ipc")
	if file == nil {
		return nil, fmt.Errorf("execd: invalid -execd-fd %d", fd)
	}
	// net.FileConn dups the descriptor (close-on-exec on the copy); the
	// inherited original must not stay open in this process, or every
	// sandboxed command it spawns inherits the host protocol channel and
	// can read or forge frames on it.
	unix.CloseOnExec(fd)
	conn, err := net.FileConn(file)
	if closeErr := file.Close(); closeErr != nil && err == nil {
		return nil, closeErr
	}
	return conn, err
}
