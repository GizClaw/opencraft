package plugins

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// TestStoreSubscribeWakesOnEffectiveMutations pins the push side of the
// register clock (see charter.go's FaceRefreshes): a subscriber runs once
// per mutation that moved the revision, never for a mutation that
// changed nothing, and stops when its cancel runs.
func TestStoreSubscribeWakesOnEffectiveMutations(t *testing.T) {
	src := t.TempDir()
	writePlugin(t, src, "p", testManifest("p", "0.1.0"), "bundle")
	s := NewStore(t.TempDir())

	var calls atomic.Int64
	cancel := s.Subscribe(func() { calls.Add(1) })

	if _, err := s.Install(filepath.Join(src, "p")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("notifications after install = %d, want 1", got)
	}

	// No state entry means enabled, so this changes nothing.
	if err := s.SetEnabled("p", true); err != nil {
		t.Fatalf("no-op enable: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("notifications after no-op enable = %d, want 1", got)
	}

	// A refused mutation notifies nobody either.
	if err := s.SetEnabled("missing", false); err == nil {
		t.Fatal("SetEnabled on an uninstalled plugin must fail")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("notifications after failed mutation = %d, want 1", got)
	}

	if err := s.SetEnabled("p", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("notifications after disable = %d, want 2", got)
	}

	// Cancel is idempotent, and a cancelled subscriber is gone.
	cancel()
	cancel()
	if err := s.Uninstall("p"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("notifications after cancel = %d, want 2", got)
	}
}

// TestStoreSubscribeRunsAfterTheLockIsReleased: notify calls out with
// the store lock released, so a subscriber may read the registry — the
// agent tool source does exactly that when it republishes.
func TestStoreSubscribeRunsAfterTheLockIsReleased(t *testing.T) {
	src := t.TempDir()
	writePlugin(t, src, "p", testManifest("p", "0.1.0"), "bundle")
	s := NewStore(t.TempDir())

	var seen []uint64
	cancel := s.Subscribe(func() {
		// Both of these take the store lock: the callback deadlocks here
		// if notify ever runs with the lock held.
		if _, err := s.List(); err != nil {
			t.Errorf("List from a subscriber: %v", err)
		}
		seen = append(seen, s.Revision())
	})
	defer cancel()

	if _, err := s.Install(filepath.Join(src, "p")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := s.SetEnabled("p", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Fatalf("subscriber saw revisions %v, want [1 2]", seen)
	}
}

// TestStoreSubscribeUnderConcurrentMutations exercises the clock's push
// path under -race: mutators and readers run together while a subscriber
// reads the registry, so a future unlocked read or a notify that calls
// out under the lock fails here.
func TestStoreSubscribeUnderConcurrentMutations(t *testing.T) {
	src := t.TempDir()
	writePlugin(t, src, "p", testManifest("p", "0.1.0"), "bundle")
	s := NewStore(t.TempDir())
	if _, err := s.Install(filepath.Join(src, "p")); err != nil {
		t.Fatalf("Install: %v", err)
	}

	var woken atomic.Int64
	cancel := s.Subscribe(func() {
		woken.Add(1)
		if _, err := s.List(); err != nil {
			t.Errorf("List from a subscriber: %v", err)
		}
	})
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				_ = s.SetEnabled("p", (i+j)%2 == 0)
				_, _ = s.List()
				_ = s.Revision()
			}
		}(i)
	}
	wg.Wait()
	if woken.Load() == 0 {
		t.Fatal("no notifications under concurrent mutations")
	}
}
