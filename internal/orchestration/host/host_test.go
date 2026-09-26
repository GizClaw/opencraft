package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/platform/wslock"
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

// TestManagerRecoveryClaimYieldsToLiveHolder pins the cross-process half
// of the same contract: when another live process holds the workspace
// (the advisory lock), this one runs no pass and says who held it — the
// checkpoints wait for the next start that owns the workspace, instead
// of being read as crash leftovers.
func TestManagerRecoveryClaimYieldsToLiveHolder(t *testing.T) {
	m := NewManager(t.TempDir())
	holder := wslock.Info{
		PID:     os.Getpid() + 1,
		Kind:    "gui",
		Started: "2026-01-01T00:00:00Z",
	}
	calls := 0
	m.acquireLease = func(_ context.Context, path, kind string) (*wslock.Handle, error) {
		calls++
		return nil, &wslock.HeldError{Path: path, Info: holder}
	}
	root := t.TempDir()
	layout := config.WorkspaceLayout{
		Root:        root,
		SessionsDir: filepath.Join(root, "sessions"),
		WorkDir:     filepath.Join(root, "work"),
	}

	report, owed := m.claimRecovery(context.Background(), layout)
	if owed {
		t.Fatalf("claimed a pass while a live process owns the workspace: %+v", report)
	}
	if report.WorkspaceHolder == "" {
		t.Fatalf("report does not name the holder: %+v", report)
	}
	if !strings.Contains(report.WorkspaceHolder, "gui") {
		t.Fatalf("holder = %q, want the holder's kind", report.WorkspaceHolder)
	}

	// The decision is memoized with the root: a second assembly reports
	// the same holder without trying the lock again.
	again, owed := m.claimRecovery(context.Background(), layout)
	if owed || calls != 1 {
		t.Fatalf("second claim owed=%v lock calls=%d, want one attempt",
			owed, calls)
	}
	if again.WorkspaceHolder != report.WorkspaceHolder {
		t.Fatalf("second report = %+v, want %+v", again, report)
	}
}

// TestManagerWorkspaceLeaseNamesTheStateRoot pins where the lock lives
// and what it records: one file per workspace state root, naming the
// process that owns it. Moving the file into the session directory (or
// dropping the holder record) would leave a process that starts second
// unable to tell a live sibling from a crashed one.
func TestManagerWorkspaceLeaseNamesTheStateRoot(t *testing.T) {
	m := NewManager(t.TempDir())
	m.SetLeaseKind("headless")
	root := t.TempDir()
	layout := config.WorkspaceLayout{
		Root:        root,
		SessionsDir: filepath.Join(root, "sessions"),
		WorkDir:     filepath.Join(root, "work"),
	}

	report, owed := m.claimRecovery(context.Background(), layout)
	if !owed {
		t.Fatalf("the first claim declined the pass: %+v", report)
	}
	info, ok := wslock.ReadInfo(filepath.Join(root, wslock.FileName))
	if !ok {
		t.Fatal("no holder recorded in the workspace state root")
	}
	if info.PID != os.Getpid() || info.Kind != "headless" {
		t.Fatalf("holder = %+v, want this process as kind headless", info)
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
	ctx := context.Background()
	layout := func(sessions string) config.WorkspaceLayout {
		root := t.TempDir()
		return config.WorkspaceLayout{
			Root:        root,
			SessionsDir: sessions,
			WorkDir:     filepath.Join(root, "work"),
		}
	}
	first := layout("/sessions")

	report, owed := m.claimRecovery(ctx, first)
	if !owed {
		t.Fatalf("first claim declined the pass: %+v", report)
	}
	if !report.At.IsZero() {
		t.Fatalf("first claim carried a finished report: %+v", report)
	}
	if report, owed := m.claimRecovery(ctx, first); owed {
		t.Fatalf("second claim owed a pass: %+v", report)
	}
	if _, owed := m.claimRecovery(ctx, layout("/other")); !owed {
		t.Fatal("a different session root was not claimed")
	}

	want := RecoveryReport{
		At:        time.Now().UTC(),
		Recovered: 2,
		Archived:  1,
		Failed:    1,
	}
	m.recordRecovery("/sessions", want)
	got, owed := m.claimRecovery(ctx, first)
	if owed {
		t.Fatal("a recorded root still owed a pass")
	}
	if !got.At.Equal(want.At) || got.Recovered != want.Recovered ||
		got.Archived != want.Archived || got.Failed != want.Failed {
		t.Fatalf("claim after record = %+v, want %+v", got, want)
	}
}
