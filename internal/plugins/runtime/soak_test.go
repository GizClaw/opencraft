//go:build soak

package runtime

import (
	"context"
	"testing"
)

// TestSoakInvokeStop cycles the capability subprocess lifecycle and
// asserts the manager's process table returns to empty (no leaks).
func TestSoakInvokeStop(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		m, _ := newTestManager(t)
		if _, err := m.Invoke(ctx, "test-plugin", "auth.begin", map[string]any{}); err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
		m.Shutdown()
		m.mu.Lock()
		n := len(m.procs)
		m.mu.Unlock()
		if n != 0 {
			t.Fatalf("cycle %d: %d processes still registered", i, n)
		}
	}
}
