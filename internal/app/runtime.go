package app

import (
	"context"
	"sync"

	"github.com/GizClaw/flowcraft/core/deploy"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
)

// RuntimeController owns the runtime lifecycle: it builds the runtime
// once from the deploy document and owns its teardown.
type RuntimeController struct {
	mu      sync.Mutex
	current *runtimecore.Runtime
}

// NewRuntimeController builds the runtime for doc.
func NewRuntimeController(
	ctx context.Context,
	doc deploy.Document,
	opts ...Option,
) (*RuntimeController, error) {
	rt, err := BuildRuntime(ctx, doc, opts...)
	if err != nil {
		return nil, err
	}
	return &RuntimeController{current: rt}, nil
}

// Runtime returns the current runtime.
func (c *RuntimeController) Runtime() *runtimecore.Runtime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// Close closes the current runtime.
func (c *RuntimeController) Close() error {
	c.mu.Lock()
	current := c.current
	c.mu.Unlock()
	if current != nil {
		_ = current.Close()
	}
	return nil
}
