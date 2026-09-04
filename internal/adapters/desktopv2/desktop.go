// Package desktopv2 is the clean-room successor to internal/adapters/
// desktop. It keeps the old implementation untouched as a behavioral
// reference until every domain has been migrated.
package desktopv2

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
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
	c.Prompt.SetRunConvResolver(c.Conversation.ConversationForRun)
	c.Runtime.Manager().SetUsageObserver(func(_ context.Context, usage inference.Usage) {
		c.Shell.Emit("usage", core.NewUsageEvent(usage))
	})
	c.Runtime.SetHostConfigurator(func(h *host.Host) {
		h.SetArtifactObserver(func(ctx context.Context, path string, data []byte) {
			if h != c.Runtime.Current() {
				return
			}
			info, ok := agent.RunInfoFromContext(ctx)
			if !ok || info.ConversationID == "" {
				return
			}
			c.Shell.Emit("artifact", map[string]any{
				"conversation_id": info.ConversationID,
				"path":            path,
				"bytes":           len(data),
			})
		})
		h.SetSessionUpdated(func(_ context.Context, contextID string) {
			if h == c.Runtime.Current() {
				c.Shell.Emit("session_updated", map[string]string{"id": contextID})
			}
		})
	})
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
		OnChange: func() {
			d.core.Shell.Emit("automation_changed", map[string]any{})
		},
		OnRun: func(run automations.Run) {
			d.core.Shell.Emit("automation_run", bindings.ToAutomationRunDTO(run))
		},
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
	current := d.core.ActiveWorkDir() != "" &&
		filepath.Clean(d.core.ActiveWorkDir()) == filepath.Clean(task.Workspace)
	h, err := d.core.Runtime.AcquireBackground(ctx, task.Workspace, interact.Auto{})
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
	runID := run.RunID()
	contextID := run.ContextID()
	if current {
		d.core.Shell.Emit("automation_run_started", map[string]any{
			"run_id":          runID,
			"conversation_id": contextID,
		})
	}
	res, waitErr := run.Wait(ctx)
	finishedAt := time.Now().UTC()
	result := automations.RunResult{
		ConversationID: contextID,
		RunID:          runID,
	}
	status := "unknown"
	errText := ""
	if res != nil {
		status = string(res.Status)
		if res.Err != nil {
			errText = res.Err.Error()
		}
	}
	if waitErr != nil && errText == "" {
		errText = waitErr.Error()
	}
	output := automationOutput(res)
	if waitErr != nil {
		result.Status = automations.RunFailed
		result.Error = errText
	} else {
		if res != nil && status == string(agent.StatusCompleted) {
			result.Status = automations.RunCompleted
		} else {
			result.Status = automations.RunFailed
			result.Error = errText
		}
	}
	notify := !suppressAutomationNotify(task, result.Status, errText)
	if current {
		end := core.NewTurnEnd(
			runID, contextID, status, errText, output, finishedAt,
		)
		end.Notify = &notify
		d.core.Shell.Emit("turn_end", end)
	} else if notify {
		d.core.Shell.Emit("automation_notify", map[string]any{
			"task_id": task.ID,
			"name":    task.Name,
			"status":  string(result.Status),
			"error":   result.Error,
			"output":  output,
		})
	}
	if waitErr != nil {
		return result, waitErr
	}
	return result, nil
}

// automationOutput returns the bounded text of the run's final
// assistant message for notifications outside the open workspace.
func automationOutput(res *agent.Result) string {
	if res == nil {
		return ""
	}
	for i := len(res.Messages) - 1; i >= 0; i-- {
		if res.Messages[i].Role != message.RoleAssistant {
			continue
		}
		text := strings.TrimSpace(res.Messages[i].Content.Text())
		if text == "" {
			continue
		}
		if len(text) > 8000 {
			text = text[len(text)-8000:]
		}
		return text
	}
	return ""
}

// suppressAutomationNotify applies the task's notification policy.
func suppressAutomationNotify(
	task automations.Task, status automations.RunStatus, errorText string,
) bool {
	switch task.Notify {
	case automations.NotifyNever:
		return true
	case automations.NotifyFailed:
		return status != automations.RunFailed && errorText == ""
	default:
		return false
	}
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
