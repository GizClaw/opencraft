// Package engine assembles and owns the lifecycle of an assembled
// flowcraft runtime. Prompt routing lives in orchestration/interact.
package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/GizClaw/flowcraft/core/deploy"
	runtimecore "github.com/GizClaw/flowcraft/core/runtime"
	"github.com/GizClaw/flowcraft/craft"
)

// Controller owns the assembly lifecycle of one Host: the Craft its
// runtime was built from, and the runtime key it was opened under.
type Controller struct {
	craft *craft.Craft
	key   craft.RuntimeKey
	// opts are the runtime options the key was opened with; a reload
	// replaces the layers and keeps the rest, so a later plain reload
	// composes the same inputs again.
	opts craft.RuntimeOptions
}

// Runtime returns the current runtime, or nil once the assembly is
// closed.
func (c *Controller) Runtime() *runtimecore.Runtime {
	if c == nil || c.craft == nil {
		return nil
	}
	rt, _ := c.craft.Runtime(c.key)
	return rt
}

// Close closes the Craft: its runtime, its services and its event bus.
func (c *Controller) Close() error {
	if c == nil || c.craft == nil {
		return nil
	}
	return c.craft.Close()
}

// Drain waits for active session turns to finish naturally without
// interrupting them, then leaves the runtime drained so Close can tear
// it down once its replacement is ready. Host uses it when an old
// runtime is invalidated but still owns in-flight turns.
func (c *Controller) Drain(ctx context.Context) error {
	if c == nil || c.craft == nil {
		return nil
	}
	err := c.craft.DrainRuntime(ctx, c.key)
	if errors.Is(err, craft.ErrRuntimeNotFound) {
		return nil
	}
	return err
}

// Reload swaps the deployment generation in place from a freshly
// loaded overlay: craft recomposes the definition's base layers with
// the overlay and the runtime swaps generations atomically. In-flight
// turns finish on the generation they started on; the next Start uses
// the new one, and the retired generation closes once its turns drain.
//
// Callers that own host-level bindings (artifact observer, agent
// lifecycle, hooks) must rebind them for the new generation; Host does
// this through the runtime rebuild lifecycle event.
//
// The result names the generation the swap installed; a failed reload
// returns no result, because no new generation is serving.
func (c *Controller) Reload(
	ctx context.Context,
	overlay []deploy.Layer,
) (*runtimecore.ReloadResult, error) {
	if c == nil || c.craft == nil {
		return nil, fmt.Errorf("engine: runtime is not ready")
	}
	opts := c.opts
	opts.Layers = overlay
	result, err := c.craft.ReloadRuntimeWith(
		ctx, c.key, opts, craft.ReasonConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("engine: reload runtime %q: %w", c.key, err)
	}
	return result, nil
}
