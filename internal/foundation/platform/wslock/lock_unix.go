//go:build !windows

package wslock

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes the exclusive advisory lock without blocking.
func lockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockFile drops the advisory lock (closing the descriptor does it too;
// this makes Release explicit).
func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

// isLockBusy reports whether err means "another process holds it".
func isLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) ||
		errors.Is(err, syscall.EAGAIN) ||
		errors.Is(err, syscall.EACCES)
}
