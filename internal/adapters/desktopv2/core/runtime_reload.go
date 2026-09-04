package core

import (
	"context"
	"strings"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// ReloadRuntime invalidates pooled Hosts and immediately reassembles
// one for the active workspace when inference is configured. Bindings
// that mutate configuration use this so the UI never sits in a state
// where Runtime.Current is nil while a workspace is open.
func (c *Core) ReloadRuntime(ctx context.Context) error {
	if err := c.Runtime.Reload(ctx); err != nil {
		return err
	}
	workDir := c.ActiveWorkDir()
	if strings.TrimSpace(workDir) == "" {
		return nil
	}
	mgr, err := config.Open(config.Options{UserDir: c.UserDir})
	if err != nil {
		return err
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		return err
	}
	configured, err := config.RouterConfigured(view.Document)
	if err != nil {
		return err
	}
	if !configured {
		return nil
	}
	_, err = c.Runtime.Acquire(ctx, workDir, interact.Auto{})
	return err
}
