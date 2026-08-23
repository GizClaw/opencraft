package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"

	app "github.com/GizClaw/opencraft/internal/app"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/setup"
)

// Version returns the application version.
func (a *App) Version() string {
	return app.ServiceVersion
}

// ConfigStatus returns the current configuration state; the frontend
// pulls it on mount and after every rebuild.
func (a *App) ConfigStatus() (ConfigStatus, error) {
	needed, err := setup.Needed(a.userDir)
	if err != nil {
		return ConfigStatus{}, err
	}
	st := a.status(!needed)
	st.Needed = needed
	return st, nil
}

// Providers returns the onboarding provider catalog.
func (a *App) Providers() []ProviderView {
	out := make([]ProviderView, 0, len(setup.Providers))
	for _, p := range setup.Providers {
		out = append(out, ProviderView{
			ID:           p.ID,
			Name:         p.Name,
			DefaultModel: p.DefaultModel,
			EnvVar:       p.EnvVar,
			API:          p.API,
			Azure:        p.Azure,
		})
	}
	return out
}

// SaveSetup writes the onboarding result into the user configuration
// layer and rebuilds the runtime.
func (a *App) SaveSetup(req SetupRequest) error {
	if len(req.Providers) == 0 {
		return errors.New("至少选择一个 provider")
	}
	cfg := setup.Config{}
	for _, p := range req.Providers {
		prov, ok := providerByID(providerID(p.ID))
		if !ok {
			return fmt.Errorf("unknown provider %q", p.ID)
		}
		keyed := setup.KeyedProvider{
			Provider:  prov,
			KeySource: setup.KeyLiteral,
			Model:     strings.TrimSpace(p.Model),
			Endpoint:  strings.TrimSpace(p.Endpoint),
			Vision:    p.Vision,
			Reasoning: strings.TrimSpace(p.Reasoning),
			WebSearch: p.WebSearch,
		}
		switch {
		case p.KeyEnv:
			keyed.KeySource = setup.KeyEnv
			if os.Getenv(prov.EnvVar) == "" {
				return fmt.Errorf("环境变量 %s 未设置，无法使用 env 方式存储密钥", prov.EnvVar)
			}
		case strings.TrimSpace(p.Key) == "":
			return fmt.Errorf("provider %s: 需要 API key 或选择环境变量方式", prov.ID)
		default:
			keyed.KeyValue = strings.TrimSpace(p.Key)
		}
		cfg.Providers = append(cfg.Providers, keyed)
	}
	if err := cfg.Write(a.userDir); err != nil {
		return err
	}
	return a.rebuild()
}

// Reload rebuilds the runtime from the current configuration.
func (a *App) Reload() error {
	return a.rebuild()
}

// Workspace returns the workspace directory the app operates on.
func (a *App) Workspace() string {
	return a.workDir
}

// StartTurn starts one assistant turn with the given user message and
// returns immediately. Stream deltas arrive over the UI event channel.
func (a *App) StartTurn(text string) (TurnStart, error) {
	a.mu.Lock()
	ctrl := a.ctrl
	broker := a.broker
	a.mu.Unlock()
	if ctrl == nil || ctrl.Runtime() == nil || broker == nil {
		return TurnStart{}, errors.New("runtime 未就绪：请先完成推理配置")
	}
	if strings.TrimSpace(text) == "" {
		return TurnStart{}, errors.New("消息不能为空")
	}
	ctx := a.appContext()

	rt := ctrl.Runtime()
	contextID := ocsessions.NewID()
	key := coresession.Key{AgentID: "assistant", ContextID: contextID}
	lease, err := rt.Sessions().Open(ctx, key)
	if err != nil {
		return TurnStart{}, fmt.Errorf("open session: %w", err)
	}
	turn, err := lease.Session().Start(ctx, agent.Request{
		ContextID: contextID,
		Message:   message.NewTextMessage(message.RoleUser, text),
	}, coresession.SinkSpec{
		ID:         "desktop",
		Sink:       agent.StreamSinkFunc(a.bridge.Sink),
		QueueSize:  256,
		Visibility: coresession.VisibilityRaw,
		Authority:  coresession.AuthorityObserver,
		AckMode:    coresession.AckOnDelivery,
	})
	if err != nil {
		_ = lease.Close()
		return TurnStart{}, fmt.Errorf("start turn: %w", err)
	}
	broker.BindTurn(turn.RunID(), turn)

	a.mu.Lock()
	a.turns[turn.RunID()] = turn
	a.mu.Unlock()
	go a.waitTurn(lease, turn)
	return TurnStart{RunID: turn.RunID(), ContextID: contextID}, nil
}

// ReplyPrompt answers one pending interaction rendered by the UI.
func (a *App) ReplyPrompt(promptID string, req ReplyRequest) (bool, error) {
	return a.bridge.Answer(promptID, req)
}

// CancelTurn cancels a running turn; the turn's terminal event still
// arrives so the UI can finalize the run.
func (a *App) CancelTurn(runID string) error {
	a.mu.Lock()
	turn := a.turns[runID]
	a.mu.Unlock()
	if turn == nil {
		return fmt.Errorf("turn %s 不存在", runID)
	}
	turn.Cancel()
	return nil
}

// ListAgents returns the persisted subagents (registry view; the
// runtime owns the live delegation targets).
func (a *App) ListAgents() []AgentSummary {
	a.mu.Lock()
	lifecycle := a.agents
	a.mu.Unlock()
	if lifecycle == nil {
		return nil
	}
	return lifecycle.List()
}

// openCommand returns the platform opener for files/directories.
func openCommand(path string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path)
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path)
	default:
		return exec.Command("xdg-open", path)
	}
}

func (a *App) waitTurn(lease *coresession.Lease, turn *coresession.Turn) {
	ctx := a.appContext()
	res, err := turn.Wait(ctx)
	runID := turn.RunID()

	a.mu.Lock()
	delete(a.turns, runID)
	if a.broker != nil {
		a.broker.UnbindTurn(runID)
	}
	a.mu.Unlock()
	_ = lease.Close()

	end := TurnEnd{RunID: runID, Status: "unknown"}
	if res != nil {
		end.Status = string(res.Status)
		if res.Err != nil {
			end.Error = res.Err.Error()
		}
	}
	if err != nil && end.Error == "" {
		end.Error = err.Error()
	}
	a.bridge.Emit("turn_end", end)
}
