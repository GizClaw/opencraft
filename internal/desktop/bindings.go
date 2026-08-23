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
	"github.com/GizClaw/opencraft/internal/config"
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

// ConfigState returns the configured inference wiring (providers in
// router priority order with their keys/models/capabilities) plus the
// current default model, so the config page can edit in place.
func (a *App) ConfigState() (ConfigState, error) {
	cfg, err := setup.Load(a.userDir)
	if err != nil {
		return ConfigState{}, err
	}
	st := ConfigState{Model: config.DefaultModel(a.userDir)}
	for _, k := range cfg.Providers {
		st.Providers = append(st.Providers, SetupProvider{
			ID:        k.Provider.ID,
			Key:       k.KeyValue,
			KeyEnv:    k.KeySource == setup.KeyEnv,
			Model:     k.Model,
			Endpoint:  k.Endpoint,
			Vision:    k.Vision,
			Reasoning: k.Reasoning,
			WebSearch: k.WebSearch,
		})
	}
	return st, nil
}

// SaveSetup writes the onboarding result into the user configuration
// layer and rebuilds the runtime.
func (a *App) SaveSetup(req SetupRequest) error {
	if len(req.Providers) == 0 {
		return errors.New("select at least one provider")
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
				return fmt.Errorf(
					"environment variable %s is not set; cannot use the env key source",
					prov.EnvVar)
			}
		case strings.TrimSpace(p.Key) == "":
			return fmt.Errorf(
				"provider %s: an API key or the env key source is required", prov.ID)
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
	contextID := a.conversationID
	mode := a.mode
	store := a.sessions
	a.mu.Unlock()
	if ctrl == nil || ctrl.Runtime() == nil || broker == nil {
		return TurnStart{}, errors.New(
			"runtime is not ready: complete the inference setup first")
	}
	if strings.TrimSpace(text) == "" {
		return TurnStart{}, errors.New("message is required")
	}
	ctx := a.appContext()

	rt := ctrl.Runtime()
	if mode.IsYOLO() && store != nil {
		if err := store.SetMode(contextID, mode); err != nil {
			return TurnStart{}, fmt.Errorf("persist permission mode: %w", err)
		}
	}
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

// NewChat starts a fresh conversation: a new session context is
// minted, so subsequent turns keep their own history and permission
// mode. The conversation resets to workspace mode.
func (a *App) NewChat() (string, error) {
	a.mu.Lock()
	a.conversationID = ocsessions.NewID()
	a.mode = ocsessions.ModeWorkspace
	id := a.conversationID
	a.mu.Unlock()
	return id, nil
}

// SessionMode returns the sandbox permission mode of the current
// conversation ("workspace" or "yolo").
func (a *App) SessionMode() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return string(a.mode), nil
}

// SetSessionMode switches the conversation's sandbox permission mode.
// The change is persisted for the conversation and therefore also
// applies to commands still running in the current turn.
func (a *App) SetSessionMode(mode string) error {
	m := ocsessions.Mode(mode)
	switch m {
	case ocsessions.ModeWorkspace, ocsessions.ModeYOLO:
	default:
		return fmt.Errorf("unknown permission mode %q", mode)
	}
	a.mu.Lock()
	a.mode = m
	contextID := a.conversationID
	store := a.sessions
	a.mu.Unlock()
	if store == nil {
		return nil
	}
	if err := store.SetMode(contextID, m); err != nil {
		return fmt.Errorf("persist permission mode: %w", err)
	}
	return nil
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
		return fmt.Errorf("turn %s not found", runID)
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

// UnregisterAgent removes a persisted subagent (draining in-flight
// delegations) and deletes its declaration directory.
func (a *App) UnregisterAgent(name string) error {
	a.mu.Lock()
	lifecycle := a.agents
	a.mu.Unlock()
	if lifecycle == nil {
		return errors.New("agent registry is not available")
	}
	return lifecycle.Remove(a.appContext(), name)
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
