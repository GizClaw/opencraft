package app

import (
	"context"
	"os"
	"sync"

	sdkdeploy "github.com/GizClaw/flowcraft/sdkx/deploy"
	runtimecore "github.com/GizClaw/flowcraft/sdkx/runtime"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/execd"
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
	workDir string
	current *runtimecore.Runtime
	envStop func()
}

// NewRuntimeController builds the initial runtime.
func NewRuntimeController(
	ctx context.Context,
	doc sdkdeploy.Document,
	opts ...Option,
) (*RuntimeController, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	mode, err := executionMode(ctx, workDir)
	if err != nil {
		return nil, err
	}
	buildOpts := opts
	var envStop func()
	if mode == config.ModeRemote {
		envClient, stop, err := execd.Launch(ctx, workDir)
		if err != nil {
			return nil, err
		}
		envStop = stop
		buildOpts = append(buildOpts,
			WithEnvironment(execd.NewRemoteEnvironment(
				"default-execd", envClient)))
	}
	rt, err := BuildRuntime(ctx, doc, buildOpts...)
	if err != nil {
		if envStop != nil {
			envStop()
		}
		return nil, err
	}
	return &RuntimeController{
		doc: doc, opts: opts, workDir: workDir,
		current: rt, envStop: envStop,
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
	mode, err := executionMode(ctx, c.workDir)
	if err != nil {
		return err
	}
	buildOpts := c.opts
	var envStop func()
	if mode == config.ModeRemote {
		envClient, stop, err := execd.Launch(ctx, c.workDir)
		if err != nil {
			return err
		}
		envStop = stop
		buildOpts = append(buildOpts,
			WithEnvironment(execd.NewRemoteEnvironment(
				"default-execd", envClient)))
	}
	rt, err := BuildRuntime(ctx, c.doc, buildOpts...)
	if err != nil {
		if envStop != nil {
			envStop()
		}
		return err
	}
	c.mu.Lock()
	old := c.current
	c.current = rt
	oldEnvStop := c.envStop
	c.envStop = envStop
	c.mu.Unlock()
	if old != nil {
		if err := old.Close(); err != nil {
			oldEnvStop()
			return err
		}
	}
	if oldEnvStop != nil {
		oldEnvStop()
	}
	return nil
}

// executionMode reads the user execution.yaml mode for workDir.
func executionMode(ctx context.Context, workDir string) (string, error) {
	mgr, err := config.Open(config.Options{WorkDir: workDir})
	if err != nil {
		return "", err
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		return "", err
	}
	if view.Execution == nil {
		return config.ModeRemote, nil
	}
	return view.Execution.Execution.Mode, nil
}

// Close closes the current runtime.
func (c *RuntimeController) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.envStop != nil {
		c.envStop()
		c.envStop = nil
	}
	if c.current != nil {
		return c.current.Close()
	}
	return nil
}
