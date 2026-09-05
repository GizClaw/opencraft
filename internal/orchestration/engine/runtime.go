// Package engine assembles and owns the lifecycle of an assembled
// flowcraft runtime. Prompt routing lives in orchestration/interact.
package engine

import (
	"context"
	"sync"

	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
)

// Controller owns the runtime lifecycle: it wraps a runtime built by
// Build and owns its teardown.
type Controller struct {
	mu      sync.Mutex
	current *runtimecore.Runtime
}

// NewController wraps a built runtime. The caller owns the runtime's
// assembly; the controller owns everything after that.
func NewController(rt *runtimecore.Runtime) *Controller {
	return &Controller{current: rt}
}

// Runtime returns the current runtime.
func (c *Controller) Runtime() *runtimecore.Runtime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// Close closes the current runtime.
func (c *Controller) Close() error {
	c.mu.Lock()
	current := c.current
	c.mu.Unlock()
	if current != nil {
		return current.Close()
	}
	return nil
}

// Drain waits for active session turns to finish naturally without
// interrupting them, then leaves the runtime drained so Close can tear
// it down once its replacement is ready. Host uses it when an old
// runtime is invalidated but still owns in-flight turns.
func (c *Controller) Drain(ctx context.Context) error {
	c.mu.Lock()
	current := c.current
	c.mu.Unlock()
	if current == nil {
		return nil
	}
	return current.Drain(ctx)
}
