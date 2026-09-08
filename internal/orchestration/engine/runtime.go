// Package engine assembles and owns the lifecycle of an assembled
// flowcraft runtime. Prompt routing lives in orchestration/interact.
package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/GizClaw/flowcraft/core/deploy"
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

// Reload atomically swaps the deployment document inside the current
// runtime. In-flight turns finish on the generation they started on;
// the next Start uses the new generation, and the retired generation
// closes once its turns drain. Callers that own host-level bindings
// (artifact observer, agent lifecycle, hooks) must rebind them for the
// new generation; Host does this through the runtime rebuild lifecycle
// event.
func (c *Controller) Reload(
	ctx context.Context,
	doc deploy.Document,
) (*runtimecore.ReloadResult, error) {
	c.mu.Lock()
	current := c.current
	c.mu.Unlock()
	if current == nil {
		return nil, fmt.Errorf("engine: runtime is not ready")
	}
	return current.Reload(ctx, doc)
}
