// Package runtime owns the opencraft runtime facade: Controller
// manages the lifecycle of an assembled flowcraft runtime, and Broker
// routes the runtime's prompt events (AskUser / permissions) to a
// pluggable UI backend through the Spec/Reply rendering contract.
package runtime

import (
	"sync"

	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
)

// Controller owns the runtime lifecycle: it wraps a runtime
// built by app.BuildRuntime and owns its teardown.
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

// Broker wires the runtime's prompt events to backend. Call Attach to
// start routing and Close when the controller closes.
func (c *Controller) Broker(backend Backend) *Broker {
	return New(c.Runtime(), backend)
}

// Close closes the current runtime.
func (c *Controller) Close() error {
	c.mu.Lock()
	current := c.current
	c.mu.Unlock()
	if current != nil {
		_ = current.Close()
	}
	return nil
}
