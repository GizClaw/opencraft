package execd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestSweepOrphansReapsDeadParentChildren pins the journal sweep: a
// child whose recorded parent is gone is killed (tree included) and its
// journal file is removed, while a live parent keeps its child.
func TestSweepOrphansReapsDeadParentChildren(t *testing.T) {
	SetJournalRoot(t.TempDir())
	nonce := "orphan-test-nonce"
	marker := "sleep 37.25"
	// The trailing `true` keeps /bin/sh from exec-replacing itself with
	// sleep, so the process keeps the nonce in its command line.
	cmd := exec.Command("/bin/sh", "-c", marker+"; true", nonce)
	configureChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killTree(cmd.Process.Pid) })

	deadline := time.Now().Add(3 * time.Second)
	for pgrepMarker(marker) == "" {
		if time.Now().After(deadline) {
			t.Fatal("marker process never started")
		}
		time.Sleep(20 * time.Millisecond)
	}

	dir, err := childrenDir()
	if err != nil {
		t.Fatal(err)
	}
	writeRecord := func(parent int) string {
		path := filepath.Join(dir,
			strconv.Itoa(cmd.Process.Pid)+"-"+nonce+".json")
		raw, err := json.Marshal(childRecord{
			PID:       cmd.Process.Pid,
			Nonce:     nonce,
			ParentPID: parent,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// A live parent (this test process) must keep its child.
	livePath := writeRecord(os.Getpid())
	sweepOrphans(context.Background())
	if pgrepMarker(marker) == "" {
		t.Fatal("sweep killed a child whose parent is alive")
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("live-parent journal was removed: %v", err)
	}

	// A dead parent (an impossible pid) must be reaped.
	deadPath := writeRecord(999999)
	sweepOrphans(context.Background())
	if out := pgrepMarker(marker); out != "" {
		t.Fatalf("orphan survived the sweep: %s", out)
	}
	if _, err := os.Stat(deadPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan journal was not removed: %v", err)
	}
}

// TestSweepOrphansDropsStaleRecords pins that a record whose child is
// gone is removed even when the recorded parent pid still exists (a
// reused pid), so the journal cannot accumulate entries forever.
func TestSweepOrphansDropsStaleRecords(t *testing.T) {
	SetJournalRoot(t.TempDir())
	dir, err := childrenDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "999999-stale.json")
	raw, err := json.Marshal(childRecord{
		PID:       999999,
		Nonce:     "stale",
		ParentPID: os.Getpid(),
		CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	sweepOrphans(context.Background())
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale journal entry was not removed: %v", err)
	}
}
