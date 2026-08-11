package app

import (
	"context"
	"sync"

	sdkdeploy "github.com/GizClaw/flowcraft/sdkx/deploy"
	runtimecore "github.com/GizClaw/flowcraft/sdkx/runtime"
)

// RuntimeController owns the runtime lifecycle and supports hot reload:
// configuration changes trigger Reload, which rebuilds the runtime from
// the same deploy document and options, swaps it in only after a
// successful build, and then closes the previous instance. Note that
// in-memory sessions are lost on reload (the session manager lives
// inside the runtime).
type RuntimeController struct {
	mu      sync.Mutex
	doc     sdkdeploy.Document
	opts    []Option
	current *runtimecore.Runtime
}

// NewRuntimeController builds the initial runtime.
func NewRuntimeController(
	ctx context.Context,
	doc sdkdeploy.Document,
	opts ...Option,
) (*RuntimeController, error) {
	rt, err := BuildRuntime(ctx, doc, opts...)
	if err != nil {
		return nil, err
	}
	return &RuntimeController{doc: doc, opts: opts, current: rt}, nil
}

// Runtime returns the current runtime.
func (c *RuntimeController) Runtime() *runtimecore.Runtime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// Reload rebuilds the runtime with the current configuration. The
// previous runtime is closed only after the new one is ready.
func (c *RuntimeController) Reload(ctx context.Context) error {
	rt, err := BuildRuntime(ctx, c.doc, c.opts...)
	if err != nil {
		return err
	}
	c.mu.Lock()
	old := c.current
	c.current = rt
	c.mu.Unlock()
	if old != nil {
		return old.Close()
	}
	return nil
}

// Close closes the current runtime.
func (c *RuntimeController) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current != nil {
		return c.current.Close()
	}
	return nil
}
