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
	// ErrConversationBusy reports a start refused because the same
	// conversation already has a live run and the start's origin does
	// not preempt it (an automation or system run stepping aside for a
	// turn the user is watching; see RunOrigin). It is deliberately not
	// a retryable lifecycle error: the live run is progressing
	// normally, and repeating the start while it lives fails the same
	// way. Scheduled callers record the run as skipped and let the
	// next occurrence try again.
	ErrConversationBusy = errors.New("host: conversation has a live run")
	// ErrNoTarget reports an acquire or ensure call that named no
	// workspace and no application (see Target). It is not a retryable
	// lifecycle error either: waiting for a replacement Host cannot
	// turn an empty path into a workspace. Callers reach it only by
	// asking for a Host before a workspace is selected.
	ErrNoTarget = errors.New("host: no target to serve")
	// ErrNoAssembly reports a request for a target kind the manager has
	// no builder for (see Target.Kind). It is not a retryable lifecycle
	// error: the gap is in the code, not in a runtime that is still
	// coming up, and retrying it would fail the same way for the whole
	// retry window. Its message names the target, and the pool never
	// falls back to another scope's builder to answer it.
	ErrNoAssembly = errors.New("host: no assembly for this target kind")
	// ErrAppNotEnabled reports an application assembly refused because
	// the registry says the application is disabled. Enablement is
	// checked where the runtime would be built — not only where the
	// choice was made — so "disabled" holds for every path that asks
	// for the application's Host, including one that asks before the
	// page invalidated it. It is not a retryable lifecycle error: no
	// replacement Host appears while the choice stands.
	ErrAppNotEnabled = errors.New("host: application is disabled")
)

// IsRetryableStartError reports whether err is one of the host
// lifecycle guards that a caller can retry after the Host pool
// assembles a replacement: runtime not ready, runtime closing, or
// session store not ready. Operational errors (invalid ids, deleted or
// deleting sessions, a busy conversation, message validation, turn
// execution) are not retryable and must surface to the user as-is.
func IsRetryableStartError(err error) bool {
	return errors.Is(err, ErrRuntimeNotReady) ||
		errors.Is(err, ErrRuntimeClosing) ||
		errors.Is(err, ErrSessionStoreNotReady)
}
