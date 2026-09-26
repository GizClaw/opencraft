// Package wslock implements the per-workspace advisory lock the crash
// recovery pass takes before it scans a workspace's run checkpoints.
//
// A run writes one checkpoint per completed wave and drops it once the
// turn is archived, so a checkpoint left at assembly time usually
// belongs to a process that died. Usually is not always: two processes
// share one workspace whenever `opencraft run` (headless) works in a
// project the desktop app also has open — a supported combination, not a
// bug. A process starting inside another process's long wave sees a live
// checkpoint with an older timestamp, and the timestamp heuristic in the
// recovery pass (docs/agent-runtime-parity/layer-1-4, "不重放 frontier")
// would materialize a running turn as interrupted.
//
// The lock answers the one question a timestamp cannot: is another live
// process holding this workspace? It is a non-blocking exclusive lock on
// <workspace state>/live.lock — flock on Unix, a LockFileEx range on
// Windows — so the kernel releases it when the holder dies for any
// reason. The file's content names the holder for diagnostics; it is the
// last writer, not a liveness oracle, only the lock state is.
package wslock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/version"
)

// FileName is the lock file inside a workspace's state root.
const FileName = "live.lock"

// Info describes one lock holder. It is written into the lock file after
// the lock is taken, so a process that finds the lock busy can say who
// holds it.
type Info struct {
	// PID is the holder's process id.
	PID int `json:"pid"`
	// Kind names the surface holding the workspace ("gui", "headless").
	Kind string `json:"kind,omitempty"`
	// Started is when the holder took the lock (RFC3339, UTC).
	Started string `json:"started_at,omitempty"`
	// Version is the holder's build version.
	Version string `json:"version,omitempty"`
}

// LiveFrom reports whether the holder recorded its own liveness window
// starting at or before t. It is a convenience for diagnostics: the
// authoritative liveness signal is the lock itself.
func (i Info) LiveFrom(t time.Time) bool {
	if i.Started == "" {
		return false
	}
	started, err := time.Parse(time.RFC3339, i.Started)
	if err != nil {
		return false
	}
	return !started.After(t)
}

// Handle is a held lock. Release it (or let the process die) to hand the
// workspace to the next process.
type Handle struct {
	path   string
	info   Info
	file   *os.File
	shared bool
}

// Path is the lock file this handle holds.
func (h *Handle) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}

// Info names the holder this handle recorded (its own process, unless
// Owned reports false).
func (h *Handle) Info() Info {
	if h == nil {
		return Info{}
	}
	return h.info
}

// Owned reports whether this handle owns the kernel lock. It is false
// when another handle in the same process already holds the workspace:
// the lock is effectively ours either way, but only the first handle
// releases it.
func (h *Handle) Owned() bool {
	return h != nil && !h.shared && h.file != nil
}

// HeldError reports that another live process holds the lock.
type HeldError struct {
	// Path is the lock file.
	Path string
	// Info is the holder as recorded in the file (zero values when the
	// file could not be read).
	Info Info
}

func (e *HeldError) Error() string {
	if e.Info.PID == 0 {
		return fmt.Sprintf("wslock: %s is held by another live process", e.Path)
	}
	return fmt.Sprintf("wslock: %s is held by pid %d (%s since %s)",
		e.Path, e.Info.PID, e.Info.Kind, e.Info.Started)
}

// ErrHeld is the sentinel behind every HeldError.
var ErrHeld = errors.New("wslock: held by another live process")

func (e *HeldError) Unwrap() error { return ErrHeld }

// IsHeld reports whether err means "another live process holds it" and
// returns that holder.
func IsHeld(err error) (Info, bool) {
	var held *HeldError
	if errors.As(err, &held) {
		return held.Info, true
	}
	return Info{}, false
}

// Acquire takes the workspace lock without blocking.
//
//   - (handle, nil): this process holds the workspace. A handle whose
//     Owned reports false means the lock was already held by another
//     handle of the same process, which is what happens when two
//     managers address one state root (tests, embedded hosts); the
//     caller may proceed, Release is a no-op.
//   - (nil, err with IsHeld(err)): another live process holds it.
//   - (nil, err): the lock could not be taken, or the lock file could
//     not be written. Callers that guard best-effort work should fail
//     open here — a filesystem that cannot lock must not silently
//     disable crash recovery — and fail closed on IsHeld.
//
// ctx only carries the diagnostic log of a close that failed after the
// lock decision was already made.
func Acquire(ctx context.Context, path, kind string) (*Handle, error) {
	if path == "" {
		return nil, errors.New("wslock: lock path is empty")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("wslock: create lock dir: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("wslock: open lock file: %w", err)
	}
	if err := lockFile(file); err != nil {
		holder := readInfo(path)
		if isLockBusy(err) {
			closeQuiet(ctx, file)
			if holder.PID == os.Getpid() {
				return &Handle{path: path, info: holder, shared: true}, nil
			}
			return nil, &HeldError{Path: path, Info: holder}
		}
		return nil, errors.Join(
			fmt.Errorf("wslock: lock %s: %w", path, err), closeLocked(file))
	}
	info := Info{
		PID:     os.Getpid(),
		Kind:    kind,
		Started: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
		Version: version.ServiceVersion,
	}
	if err := writeInfo(file, info); err != nil {
		return nil, errors.Join(
			fmt.Errorf("wslock: record holder: %w", err), closeLocked(file))
	}
	return &Handle{path: path, info: info, file: file}, nil
}

// Release drops the lock. The lock file stays behind with the last
// holder's record, which is what makes a later "who held this" question
// answerable; only the kernel lock decides liveness.
func (h *Handle) Release() error {
	if h == nil || h.file == nil {
		return nil
	}
	err := unlockFile(h.file)
	closeErr := h.file.Close()
	h.file = nil
	return errors.Join(err, closeErr)
}

// ReadInfo returns the last holder recorded in a lock file, if any. It
// reads the file only: the result says nothing about liveness.
func ReadInfo(path string) (Info, bool) {
	info := readInfo(path)
	return info, info.PID != 0
}

// readInfo decodes the holder record, tolerating a truncated or
// half-written file (the writer holds the lock, so a reader that lost
// the race sees the previous record or nothing). Reading by path keeps
// the read side descriptor-free: a probe that cannot close its own
// handle is worse than no probe at all.
func readInfo(path string) Info {
	if path == "" {
		return Info{}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Info{}
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return Info{}
	}
	return info
}

// writeInfo records the holder and makes it visible to the next process
// that finds the lock busy.
func writeInfo(file *os.File, info Info) error {
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

// closeLocked closes a lock file descriptor and hands its error back, so
// the callers that are already returning one can carry it.
func closeLocked(file *os.File) error {
	if file != nil {
		return file.Close()
	}
	return nil
}

// closeQuiet closes a descriptor whose error cannot change the caller's
// answer: the lock decision has been made, and the kernel lock - not the
// descriptor - is what decides who owns the workspace. A genuinely
// surprising failure is recorded, not dropped.
func closeQuiet(ctx context.Context, file *os.File) {
	if file == nil {
		return
	}
	name := file.Name()
	err := file.Close()
	if err == nil || errors.Is(err, os.ErrClosed) {
		return
	}
	telemetry.WarnErr(ctx, "wslock: close lock file failed", err,
		otellog.String("path", name))
}
