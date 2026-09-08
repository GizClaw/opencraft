package host

import "errors"

// Retryable host lifecycle errors. Host guards return these before any
// turn, fork, import, reload or delete side effect runs, so adapters
// may wait for the replacement Host and retry the operation safely.
var (
	// ErrRuntimeNotReady reports a Host whose flowcraft controller is
	// not usable yet.
	ErrRuntimeNotReady = errors.New("host: runtime is not ready")
	// ErrRuntimeClosing reports a Host that is retiring: it stopped
	// accepting new turns while its live runs drain and tear down.
	ErrRuntimeClosing = errors.New("host: runtime is closing")
	// ErrSessionStoreNotReady reports a Host whose shared session
	// store is not usable yet.
	ErrSessionStoreNotReady = errors.New("host: session store is not ready")
)

// IsRetryableStartError reports whether err is one of the host
// lifecycle guards that a caller can retry after the Host pool
// assembles a replacement: runtime not ready, runtime closing, or
// session store not ready. Operational errors (invalid ids, deleted or
// deleting sessions, message validation, turn execution) are not
// retryable and must surface to the user as-is.
func IsRetryableStartError(err error) bool {
	return errors.Is(err, ErrRuntimeNotReady) ||
		errors.Is(err, ErrRuntimeClosing) ||
		errors.Is(err, ErrSessionStoreNotReady)
}
