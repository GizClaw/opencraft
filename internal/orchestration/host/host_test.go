package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePoolSharesPerRoot(t *testing.T) {
	m := NewManager(t.TempDir())
	ctx := context.Background()
	workDir := filepath.Join(t.TempDir(), "repo")
	root := filepath.Join(t.TempDir(), "sessions")
	a, err := m.acquireStore(ctx, workDir, root, 40)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.acquireStore(ctx, workDir, root, 40)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("same workspace root must share one Store")
	}
	m.releaseStore(a)
	m.releaseStore(b)
}

func TestStorePoolClosesAfterLastRelease(t *testing.T) {
	m := NewManager(t.TempDir())
	ctx := context.Background()
	workDir := filepath.Join(t.TempDir(), "repo")
	root := filepath.Join(t.TempDir(), "sessions")
	a, err := m.acquireStore(ctx, workDir, root, 40)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.acquireStore(ctx, workDir, root, 40)
	if err != nil {
		t.Fatal(err)
	}
	m.releaseStore(a)
	m.releaseStore(b)
	m.mu.Lock()
	_, exists := m.stores[filepath.Clean(root)]
	m.mu.Unlock()
	if exists {
		t.Fatal("store must be removed after the last release")
	}
}

func TestAcquireStoreAdoptsLegacyProjectSessions(t *testing.T) {
	m := NewManager(t.TempDir())
	ctx := context.Background()
	workDir := t.TempDir()
	root := filepath.Join(t.TempDir(), "sessions")
	legacyRoot := filepath.Join(workDir, ".opencraft", "sessions")
	id := "s-legacy1"
	historyDir := filepath.Join(legacyRoot, id, "history")
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(historyDir, "000001.json"),
		[]byte(`{"seq":1,"at":"2026-01-01T00:00:00Z","messages":[{"role":"user","content":{"parts":[{"type":"text","text":"legacy hello"}]}}]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	store, err := m.acquireStore(ctx, workDir, root, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer m.releaseStore(store)

	metas, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, meta := range metas {
		if meta.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("migrated session %s missing from %+v", id, metas)
	}
	if _, err := os.Stat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy sessions root still present: %v", err)
	}
}

// TestManagerRecoveryClaimSharesOnePassPerRoot pins the contract the
// recovery pass runs under: one scan per session root per process, and
// the Host a runtime reload assembles afterwards reports the pass its
// predecessor ran instead of claiming none happened.
func TestManagerRecoveryClaimSharesOnePassPerRoot(t *testing.T) {
	m := NewManager(t.TempDir())

	report, owed := m.claimRecovery("/sessions")
	if !owed {
		t.Fatalf("first claim declined the pass: %+v", report)
	}
	if !report.At.IsZero() {
		t.Fatalf("first claim carried a finished report: %+v", report)
	}
	if report, owed := m.claimRecovery("/sessions"); owed {
		t.Fatalf("second claim owed a pass: %+v", report)
	}
	if _, owed := m.claimRecovery("/other"); !owed {
		t.Fatal("a different session root was not claimed")
	}

	want := RecoveryReport{
		At:        time.Now().UTC(),
		Recovered: 2,
		Archived:  1,
		Failed:    1,
	}
	m.recordRecovery("/sessions", want)
	got, owed := m.claimRecovery("/sessions")
	if owed {
		t.Fatal("a recorded root still owed a pass")
	}
	if !got.At.Equal(want.At) || got.Recovered != want.Recovered ||
		got.Archived != want.Archived || got.Failed != want.Failed {
		t.Fatalf("claim after record = %+v, want %+v", got, want)
	}
}
