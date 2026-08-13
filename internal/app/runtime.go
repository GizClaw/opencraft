package app

import (
	"context"
	"os"
	"sync"

	"github.com/GizClaw/flowcraft/core/deploy"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
)

// RuntimeController owns the runtime lifecycle and supports hot reload:
// configuration changes trigger Reload, which rebuilds the runtime from
// the same deploy document and options, swaps it in only after a
// successful build, and then closes the previous instance.
type RuntimeController struct {
	mu      sync.Mutex
	doc     deploy.Document
	opts    []Option
	workDir string
	current *runtimecore.Runtime
}

// NewRuntimeController builds the initial runtime.
func NewRuntimeController(
	ctx context.Context,
	doc deploy.Document,
	opts ...Option,
) (*RuntimeController, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	rt, err := BuildRuntime(ctx, doc, opts...)
	if err != nil {
		return nil, err
	}
	return &RuntimeController{
		doc: doc, opts: opts, workDir: workDir,
		current: rt,
	}, nil
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
		if err := old.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Close stops the execd child (if any) and closes the current runtime.
func (c *RuntimeController) Close() error {
	c.mu.Lock()
	current := c.current
	c.mu.Unlock()
	if current != nil {
		_ = current.Close()
	}
	return nil
}
