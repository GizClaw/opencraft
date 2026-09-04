// Package desktopv2 is the clean-room successor to internal/adapters/
// desktop. It keeps the old implementation untouched as a behavioral
// reference until every domain has been migrated.
package desktopv2

import (
	"context"
	"time"

	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/bindings"
	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/automations"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// Options configures the desktopv2 application.
type Options struct {
	WorkDir         string
	UserDir         string
	DataDir         string
	TrayIcon        []byte
	TrayIconWindows []byte
}

// Desktop is the desktopv2 composition root. It is not a Wails binding
// object; Bindings returns the per-domain API objects.
type Desktop struct {
	core            *core.Core
	trayIcon        []byte
	trayIconWindows []byte
}

// New resolves the user data/config directories and builds the core
// service composition.
func New(opts Options) (*Desktop, error) {
	if opts.DataDir == "" {
		dir, err := config.UserDataDir()
		if err != nil {
			return nil, err
		}
		opts.DataDir = dir
	}
	if opts.UserDir == "" {
		dir, err := config.UserConfigDir()
		if err != nil {
			return nil, err
		}
		opts.UserDir = dir
	}
	if _, err := config.EnsureUserConfig(); err != nil {
		return nil, err
	}
	c := core.NewCore(opts.UserDir, opts.DataDir, opts.WorkDir)
	c.Prompt.SetNotifier(c.Shell.Emit)
	c.SetWorkDir(c.InitialWorkDir(opts.WorkDir))
	return &Desktop{
		core:            c,
		trayIcon:        opts.TrayIcon,
		trayIconWindows: opts.TrayIconWindows,
	}, nil
}

// Startup wires the Wails context into the core shell.
func (d *Desktop) Startup(ctx context.Context) {
	d.core.Shell.SetContext(ctx)
	if err := d.core.Runtime.OpenUserDB(ctx); err == nil {
		d.startAutomations(ctx)
	}
	if err := d.core.ReloadRuntime(ctx); err != nil {
		d.core.Shell.Emit("fatal", map[string]any{"error": err.Error()})
	}
	d.core.Shell.StartTray(d.trayIcon, d.trayIconWindows)
}

// Shutdown releases runtime-owned resources. Runtime service teardown
// is added as the runtime domain migrates.
func (d *Desktop) Shutdown(context.Context) {
	d.core.Shell.StopTray()
	if mgr := d.core.Runtime.AutomationManager(); mgr != nil {
		mgr.Stop()
	}
	d.core.Runtime.Close()
	d.core.Plugin.Close()
}

func (d *Desktop) startAutomations(ctx context.Context) {
	store := d.core.Runtime.Automations()
	if store == nil {
		return
	}
	mgr, err := automations.NewManager(store, automations.ManagerOptions{
		Run:    d.runAutomation,
		Window: 2 * time.Minute,
		Limit:  4,
	})
	if err != nil {
		return
	}
	d.core.Runtime.SetAutomationManager(mgr)
	mgr.Start()
}

func (d *Desktop) runAutomation(
	ctx context.Context, task automations.Task,
) (automations.RunResult, error) {
	mode := sessions.Mode(task.Mode)
	if mode == "" {
		mode = sessions.ModeWorkspace
	}
	h, err := d.core.Runtime.Acquire(ctx, task.Workspace, interact.Auto{})
	if err != nil {
		return automations.RunResult{Status: automations.RunFailed}, err
	}
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:   message.NewTextMessage(message.RoleUser, task.Prompt),
		ContextID: task.ConversationID,
		Mode:      mode,
		Think:     task.Think,
		Model:     task.Model,
		Backend:   interact.Auto{},
	})
	if err != nil {
		return automations.RunResult{Status: automations.RunFailed}, err
	}
	res, waitErr := run.Wait(ctx)
	result := automations.RunResult{
		ConversationID: run.ContextID(),
		RunID:          run.RunID(),
	}
	if waitErr != nil {
		result.Status = automations.RunFailed
		result.Error = waitErr.Error()
		return result, waitErr
	}
	if res != nil && res.Status == "completed" {
		result.Status = automations.RunCompleted
	} else {
		result.Status = automations.RunFailed
		if res != nil && res.Err != nil {
			result.Error = res.Err.Error()
		}
	}
	return result, nil
}

// Bindings returns the Wails binding objects.
func (d *Desktop) Bindings() []interface{} {
	return []interface{}{
		bindings.NewLifecycle(d.core),
		bindings.NewConfig(d.core),
		bindings.NewWorkspace(d.core),
		bindings.NewConversationBinding(d.core),
		bindings.NewSessionBinding(d.core),
		bindings.NewAgentBinding(d.core),
		bindings.NewFileBinding(d.core),
		bindings.NewSettingsBinding(d.core),
		bindings.NewDiagnosticsBinding(d.core),
		bindings.NewPluginBinding(d.core),
		bindings.NewSecretBinding(d.core),
		bindings.NewAutomationBinding(d.core),
	}
}

// Lifecycle exposes the lifecycle binding for main.go's window close
// and second-instance handlers.
func (d *Desktop) Lifecycle() *bindings.Lifecycle {
	return bindings.NewLifecycle(d.core)
}
