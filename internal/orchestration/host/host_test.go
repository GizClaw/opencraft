package host

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStorePoolSharesPerRoot(t *testing.T) {
	m := NewManager(t.TempDir())
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "sessions")
	a, err := m.acquireStore(ctx, root, 40)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.acquireStore(ctx, root, 40)
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
	root := filepath.Join(t.TempDir(), "sessions")
	a, err := m.acquireStore(ctx, root, 40)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.acquireStore(ctx, root, 40)
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
