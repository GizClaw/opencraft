package execd

// Orphan journal: every launched child leaves one file in
// <user data>/execd/children describing itself (pid, nonce, parent).
// The channel-EOF path makes children exit with the parent, so the
// journal only matters for a child that survived a SIGKILLed host (a
// wedged read loop, a stopped process): the next launch sweeps it and
// kills the process tree.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"
)

const childrenDirName = "children"

// childRecord is one journal entry.
type childRecord struct {
	PID       int    `json:"pid"`
	Nonce     string `json:"nonce"`
	ParentPID int    `json:"parentPid"`
	// CreatedAt is the child's fork time in unix milliseconds. The
	// sweep uses it to tell our child from an unrelated process that
	// inherited the pid.
	CreatedAt int64 `json:"createdAt,omitempty"`
}

// journalRoot is the user data root the journal lives under. The host
// assembly injects it (SetJournalRoot), so this package never resolves
// the user's data directory itself.
var journalRoot atomic.Pointer[string]

// SetJournalRoot installs the user data root the orphan journal lives
// under (<root>/execd/children). Host assembly calls it.
func SetJournalRoot(root string) { journalRoot.Store(&root) }

var errJournalRootUnset = errors.New("execd: journal root is not configured")

func childrenDir() (string, error) {
	root := journalRoot.Load()
	if root == nil || *root == "" {
		return "", errJournalRootUnset
	}
	dir := filepath.Join(*root, "execd", childrenDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// recordChild writes the journal entry for one launched child and
// returns the removal function stop calls.
func recordChild(ctx context.Context, pid int, nonce string) func() {
	dir, err := childrenDir()
	if err != nil {
		if !errors.Is(err, errJournalRootUnset) {
			telemetry.WarnErr(ctx, "execd: journal directory failed", err)
		}
		return func() {}
	}
	path := filepath.Join(dir, strconv.Itoa(pid)+"-"+nonce+".json")
	raw, err := json.Marshal(childRecord{
		PID:       pid,
		Nonce:     nonce,
		ParentPID: os.Getpid(),
		CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		telemetry.WarnErr(ctx, "execd: journal encode failed", err)
		return func() {}
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		telemetry.WarnErr(ctx, "execd: journal write failed", err)
		return func() {}
	}
	return func() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			telemetry.WarnErr(context.Background(),
				"execd: remove journal entry failed", err)
		}
	}
}

var sweepOnce sync.Once

// sweepOrphansOnce reaps children left behind by an earlier host. It
// runs once per process, right before the first fork.
func sweepOrphansOnce(ctx context.Context) {
	sweepOnce.Do(func() { sweepOrphans(ctx) })
}

func sweepOrphans(ctx context.Context) {
	dir, err := childrenDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var record childRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				telemetry.WarnErr(ctx, "execd: remove corrupt journal failed", err)
			}
			continue
		}
		// A live parent owns its child; leave it alone.
		alive := processAlive(record.PID)
		if alive && record.ParentPID > 0 && processAlive(record.ParentPID) {
			continue
		}
		reaped := false
		if alive && isExecdChild(record) {
			killTree(record.PID)
			reaped = true
		}
		if reaped {
			telemetry.Warn(ctx, "execd: reaped orphaned child",
				otellog.Int("execd.pid", record.PID),
				otellog.Int("execd.parent_pid", record.ParentPID))
		}
		if !alive || reaped {
			// The child is gone (or we just reaped it), so the record
			// is stale no matter what its parent pid says now.
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				telemetry.WarnErr(ctx, "execd: journal remove failed", err)
			}
		}
		// A live process we could not identify keeps its record: it may
		// be a reused pid, and dropping the file would lose the chance
		// to reap the real orphan later.
	}
}
