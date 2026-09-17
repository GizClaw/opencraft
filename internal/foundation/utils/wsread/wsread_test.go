package wsread

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/workspace"
)

// plainWorkspace implements only the base interface, so Capped must
// take the full-read fallback.
type plainWorkspace struct {
	workspace.Workspace
	data []byte
}

func (w *plainWorkspace) Read(context.Context, string) ([]byte, error) {
	return w.data, nil
}

// boundedWorkspace implements the optional limited reader and records
// the cap it was asked for.
type boundedWorkspace struct {
	workspace.Workspace
	data  []byte
	limit int64
}

func (w *boundedWorkspace) Read(context.Context, string) ([]byte, error) {
	return w.data, nil
}

func (w *boundedWorkspace) ReadLimited(
	_ context.Context, _ string, max int64,
) ([]byte, error) {
	w.limit = max
	return w.data, nil
}

func TestCappedPrefersBoundedReads(t *testing.T) {
	ws := &boundedWorkspace{data: []byte("abcd")}
	data, err := Capped(context.Background(), ws, "image.png", 4)
	if err != nil {
		t.Fatalf("Capped: %v", err)
	}
	if string(data) != "abcd" {
		t.Fatalf("data = %q, want abcd", data)
	}
	if ws.limit != 4 {
		t.Fatalf("bounded read limit = %d, want 4", ws.limit)
	}
}

func TestCappedRejectsOversizedReads(t *testing.T) {
	ctx := context.Background()
	// A bounded backend that ignores its cap must not reach the caller.
	bounded := &boundedWorkspace{data: []byte("abcde")}
	if _, err := Capped(ctx, bounded, "image.png", 4); err == nil ||
		!strings.Contains(err.Error(), "over the 4-byte read cap") {
		t.Fatalf("bounded error = %v, want an over-cap rejection", err)
	}
	// A backend without bounded reads is checked after the read.
	plain := &plainWorkspace{data: []byte("abcde")}
	if _, err := Capped(ctx, plain, "image.png", 4); err == nil ||
		!strings.Contains(err.Error(), "over the 4-byte read cap") {
		t.Fatalf("fallback error = %v, want an over-cap rejection", err)
	}
}

func TestCappedRejectsNonPositiveCap(t *testing.T) {
	ws := &plainWorkspace{data: []byte("a")}
	if _, err := Capped(context.Background(), ws, "image.png", 0); err == nil {
		t.Fatal("a non-positive cap must be rejected")
	}
}
