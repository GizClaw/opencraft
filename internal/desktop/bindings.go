package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"
	"github.com/GizClaw/flowcraft/core/tool/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/usage"
)

// Version returns the application version.
func (a *App) Version() string {
	return app.ServiceVersion
}

// ConfigStatus returns the current configuration state; the frontend
// pulls it on mount and after every rebuild.
func (a *App) ConfigStatus() (ConfigStatus, error) {
	needed, err := config.InferenceNeeded(a.userDir)
	if err != nil {
		return ConfigStatus{}, err
	}
	st := a.status(!needed)
	st.Needed = needed
	return st, nil
}

// Providers returns the provider catalog.
func (a *App) Providers() []ProviderView {
	out := make([]ProviderView, 0, len(config.Providers))
	for _, p := range config.Providers {
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
	cfg, err := config.LoadInference(a.userDir)
	if err != nil {
		return ConfigState{}, err
	}
	st := ConfigState{Model: config.DefaultModel(a.userDir)}
	for _, in := range cfg.Instances {
		st.Instances = append(st.Instances, ProviderInstance{
			StableID: in.StableID,
			Type:     in.Type,
			Name:     in.Name,
			API:      in.API,
			// Never echo the stored secret back to the renderer; the
			// settings page only learns whether a key exists.
			KeySet: in.KeySource == config.KeyEnv ||
				(in.KeySource == config.KeyLiteral && in.KeyValue != ""),
			KeyEnv:    in.KeySource == config.KeyEnv,
			Model:     in.Model,
			Endpoint:  in.Endpoint,
			Vision:    in.Vision,
			Reasoning: in.Reasoning,
			WebSearch: in.WebSearch,
			Enabled:   in.Enabled,
		})
	}
	return st, nil
}

// ModelOptions returns the selectable per-conversation model hints in
// router priority order: every configured provider's model as
// "provider/name". The empty option (default routing policy) is implied
// by the UI and not listed here.
func (a *App) ModelOptions() ([]ModelOption, error) {
	cfg, err := config.LoadInference(a.userDir)
	if err != nil {
		return nil, err
	}
	out := make([]ModelOption, 0, len(cfg.Instances))
	for i, in := range cfg.Instances {
		if !in.Enabled {
			continue
		}
		model := strings.TrimSpace(in.Model)
		if model == "" {
			continue
		}
		out = append(out, ModelOption{
			ID:    instanceIDFor(in.Type, i+1) + "/" + model,
			Label: instanceLabel(in, i+1) + " · " + model,
		})
	}
	return out, nil
}

// ModelUsage returns per-model token usage across every workspace and
// session, most used first.
func (a *App) ModelUsage() ([]ModelUsageStat, error) {
	a.mu.Lock()
	store := a.usage
	a.mu.Unlock()
	if store == nil {
		return []ModelUsageStat{}, nil
	}
	rows, err := store.Summary(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]ModelUsageStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, ModelUsageStat{
			Model:           r.Model,
			InputTokens:     r.InputTokens,
			OutputTokens:    r.OutputTokens,
			CacheReadTokens: r.CacheReadTokens,
			ReasoningTokens: r.ReasoningTokens,
			LatencyMs:       r.LatencyMs,
			Workspaces:      r.Workspaces,
			Sessions:        r.Sessions,
			UpdatedAt:       r.UpdatedAt,
		})
	}
	return out, nil
}

// ModelUsageSeries returns one model's usage bucketed by hour or day,
// oldest first. utcOffsetMinutes places day boundaries in the viewer's
// local timezone; start and end bound the recorded UTC hours
// ([start, end), empty means unbounded).
func (a *App) ModelUsageSeries(
	model string,
	granularity string,
	utcOffsetMinutes int,
	start, end string,
) ([]UsagePoint, error) {
	a.mu.Lock()
	store := a.usage
	a.mu.Unlock()
	if store == nil || model == "" {
		return []UsagePoint{}, nil
	}
	g := usage.GranularityHour
	if granularity == string(usage.GranularityDay) {
		g = usage.GranularityDay
	}
	rows, err := store.Series(
		context.Background(),
		model,
		g,
		utcOffsetMinutes,
		start,
		end,
	)
	if err != nil {
		return nil, err
	}
	out := make([]UsagePoint, 0, len(rows))
	for _, p := range rows {
		out = append(out, UsagePoint{
			Time:            p.Time,
			InputTokens:     p.InputTokens,
			OutputTokens:    p.OutputTokens,
			CacheReadTokens: p.CacheReadTokens,
			ReasoningTokens: p.ReasoningTokens,
		})
	}
	return out, nil
}

// SaveInstances writes the inference instance configuration into the
// user configuration layer (merging over manual resources) and
// rebuilds the runtime.
func (a *App) SaveInstances(req InferenceRequest) error {
	// Existing instances let an empty key ("leave blank to keep")
	// inherit the stored key instead of forcing a re-entry. A missing
	// or unparseable config is treated as a fresh install.
	existing, _ := config.LoadInference(a.userDir)
	claimed := make(map[int]bool)
	type keyedRow struct {
		idx      int    // position in instances
		name     string // request display name (error messages)
		typ      string // catalog id
		required bool   // enabled rows must end up with a key
	}
	var pending []keyedRow
	instances := make([]config.Instance, 0, len(req.Instances))
	for _, p := range req.Instances {
		prov, ok := providerByID(strings.TrimSpace(p.Type))
		if !ok {
			return fmt.Errorf("unknown provider type %q", p.Type)
		}
		in := config.Instance{
			StableID:  strings.TrimSpace(p.StableID),
			Type:      prov.ID,
			Name:      strings.TrimSpace(p.Name),
			API:       strings.TrimSpace(p.API),
			Model:     strings.TrimSpace(p.Model),
			Endpoint:  strings.TrimSpace(p.Endpoint),
			Vision:    p.Vision,
			Reasoning: strings.TrimSpace(p.Reasoning),
			WebSearch: p.WebSearch,
			Enabled:   p.Enabled,
			KeySource: config.KeyLiteral,
		}
		if in.StableID == "" {
			// A row without an identity is brand new in the settings
			// page; give it one so the next save matches by id.
			in.StableID = config.NewStableID()
		}
		switch {
		case p.KeyEnv:
			in.KeySource = config.KeyEnv
			if os.Getenv(prov.EnvVar) == "" {
				return fmt.Errorf(
					"environment variable %s is not set; cannot use the env key source",
					prov.EnvVar)
			}
		case strings.TrimSpace(p.Key) != "":
			in.KeyValue = strings.TrimSpace(p.Key)
		case p.Enabled:
			pending = append(pending, keyedRow{
				idx:      len(instances),
				name:     p.Name,
				typ:      prov.ID,
				required: true,
			})
		case strings.TrimSpace(p.StableID) != "":
			// Disabled rows with a persisted identity keep their stored
			// key too, so re-enabling needs no re-entry. Unlike enabled
			// rows, a missing stored key is not an error: the row stays
			// declared without one.
			pending = append(pending, keyedRow{
				idx:  len(instances),
				name: p.Name,
				typ:  prov.ID,
			})
		default:
			// Disabled instances may be saved without a key; they are
			// kept so re-enabling needs no re-entry.
		}
		instances = append(instances, in)
	}
	if len(pending) > 0 {
		rows := make([]config.KeyRequest, len(pending))
		for i, r := range pending {
			rows[i] = config.KeyRequest{
				StableID: strings.TrimSpace(req.Instances[r.idx].StableID),
				Type:     r.typ,
				Name:     strings.TrimSpace(req.Instances[r.idx].Name),
				Model:    strings.TrimSpace(req.Instances[r.idx].Model),
				Endpoint: strings.TrimSpace(req.Instances[r.idx].Endpoint),
				API:      strings.TrimSpace(req.Instances[r.idx].API),
			}
		}
		idxs, ok := config.MatchStoredKeys(existing.Instances, rows, claimed)
		if !ok {
			for i, idx := range idxs {
				if idx >= 0 || !pending[i].required {
					continue
				}
				return fmt.Errorf(
					"instance %s (%s): an API key or the env key source is required",
					pending[i].name, pending[i].typ)
			}
		}
		for i, idx := range idxs {
			if idx < 0 {
				// Optional row (disabled, no stored key) stays keyless.
				continue
			}
			dst := &instances[pending[i].idx]
			dst.KeySource = existing.Instances[idx].KeySource
			dst.KeyValue = existing.Instances[idx].KeyValue
		}
	}
	cfg := config.InferenceConfig{Instances: instances}
	if len(cfg.Enabled()) == 0 {
		return errors.New("enable at least one instance")
	}
	if err := config.WriteInference(a.userDir, cfg); err != nil {
		return err
	}
	return a.rebuild()
}

// instanceIDFor derives the deployment id for instance n (1-based).
func instanceIDFor(instanceType string, n int) string {
	return fmt.Sprintf("%s-%d", instanceType, n)
}

// instanceLabel returns the display label for one instance.
func instanceLabel(in config.Instance, n int) string {
	if in.Name != "" {
		return in.Name
	}
	return instanceIDFor(in.Type, n)
}

// MCPConfig returns the configured MCP tool servers from the user
// configuration layer.
func (a *App) MCPConfig() ([]config.MCPServer, error) {
	return config.LoadMCP(a.userDir)
}

// SaveMCP persists the MCP tool server list into the user
// configuration layer (merging over manual resources) and rebuilds the
// runtime.
func (a *App) SaveMCP(servers []config.MCPServer) error {
	for i := range servers {
		if err := validateMCPServer(&servers[i]); err != nil {
			return err
		}
	}
	if err := config.WriteMCP(a.userDir, servers); err != nil {
		return err
	}
	return a.rebuild()
}

// validateMCPServer normalizes and validates one MCP server entry the
// same way the save path does, so TestMCP and SaveMCP agree on what is
// a well-formed server.
func validateMCPServer(srv *config.MCPServer) error {
	srv.Name = strings.TrimSpace(srv.Name)
	srv.Transport = strings.TrimSpace(srv.Transport)
	srv.Command = strings.TrimSpace(srv.Command)
	srv.URL = strings.TrimSpace(srv.URL)
	switch srv.Transport {
	case "stdio":
		if srv.Command == "" {
			return fmt.Errorf("MCP server %q: command is required for stdio", srv.Name)
		}
	case "http":
		if srv.URL == "" {
			return fmt.Errorf("MCP server %q: url is required for http", srv.Name)
		}
	default:
		return fmt.Errorf("MCP server %q: transport must be stdio or http", srv.Name)
	}
	if srv.Name == "" {
		return errors.New("MCP server: name is required")
	}
	return nil
}

// mcpTransport builds the MCP transport for one server entry the same
// way the runtime factory wires stdio/http servers.
func mcpTransport(server config.MCPServer) (mcpsdk.Transport, error) {
	switch server.Transport {
	case "stdio":
		return mcp.Stdio(server.Command, server.Args, server.Env)
	case "http":
		return mcp.StreamableHTTP(server.URL, nil, nil)
	default:
		return nil, fmt.Errorf("MCP server %q: transport must be stdio or http", server.Name)
	}
}

// TestMCP verifies that one MCP server can connect and expose its
// tools using a throwaway source, so neither the live runtime nor the
// saved configuration is touched. It waits up to 15s for the first
// connection; the returned error is the test result.
func (a *App) TestMCP(server config.MCPServer) error {
	if err := validateMCPServer(&server); err != nil {
		return err
	}
	const timeout = 15 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	src := mcp.NewSource(mcp.WithConnectTimeout(timeout))
	defer func() { _ = src.Close() }()
	transport, err := mcpTransport(server)
	if err != nil {
		return err
	}
	if err := src.AddServer(ctx, server.Name, transport); err != nil {
		return err
	}
	return src.WaitReady(ctx, server.Name, timeout)
}

// MCPStatus probes the live runtime's MCP source and reports each
// configured server's connection state: connected (tools published),
// connecting (first attempt or background retry pending), or error
// (the background loop gave up on it).
func (a *App) MCPStatus() ([]MCPStatusDTO, error) {
	servers, err := config.LoadMCP(a.userDir)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	ctrl := a.ctrl
	a.mu.Unlock()
	var src *mcp.Source
	if ctrl != nil && ctrl.Runtime() != nil {
		if value, ok := ctrl.Runtime().Resource("tool.mcp"); ok {
			if s, ok := value.(*mcp.Source); ok {
				src = s
			}
		}
	}
	out := make([]MCPStatusDTO, 0, len(servers))
	for _, srv := range servers {
		dto := MCPStatusDTO{Name: srv.Name}
		if src == nil {
			dto.Status = "connecting" // runtime is being assembled
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			probeErr := src.WaitReady(ctx, srv.Name, 200*time.Millisecond)
			cancel()
			switch {
			case probeErr == nil:
				dto.Status = "connected"
			case errdefs.IsTimeout(probeErr):
				dto.Status = "connecting"
			default:
				dto.Status = "error"
				dto.Error = probeErr.Error()
			}
		}
		out = append(out, dto)
	}
	return out, nil
}

// Reload rebuilds the runtime from the current configuration.
func (a *App) Reload() error {
	return a.rebuild()
}

// Workspace returns the workspace directory the app operates on.
func (a *App) Workspace() string {
	return a.snapshotWorkDir()
}

// StartTurn starts one assistant turn with the given user message and
// returns immediately. Stream deltas arrive over the UI event channel.
func (a *App) StartTurn(text string) (TurnStart, error) {
	a.mu.Lock()
	ctrl := a.ctrl
	broker := a.broker
	contextID := a.conversationID
	mode := a.mode
	think := a.think
	model := a.model
	store := a.sessions
	a.mu.Unlock()
	if ctrl == nil || ctrl.Runtime() == nil || broker == nil {
		return TurnStart{}, errors.New(
			"runtime is not ready: configure inference in Settings first")
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
	// The session runs ephemeral: opencraft owns conversation
	// durability (its own archive + session.db), so the core session
	// must not write run checkpoints that nothing can resume (runtime
	// resume is disabled) and DeleteSession would never clean.
	turn, err := lease.Session().StartWithOptions(ctx, agent.Request{
		ContextID: contextID,
		Message:   message.NewTextMessage(message.RoleUser, text),
		// Think level rides the board into the graph's
		// ${board.think_level} inference node reference.
		Inputs: map[string]any{
			"think_level": think,
			// Model hint rides the board into the graph's
			// ${board.model} inference node reference; the router
			// falls back to the default policy when it is empty.
			"model": model,
		},
	},
		coresession.WithEphemeral(),
		coresession.WithSinks(coresession.SinkSpec{
			ID:         "desktop",
			Sink:       agent.StreamSinkFunc(a.bridge.Sink),
			QueueSize:  256,
			Visibility: coresession.VisibilityRaw,
			Authority:  coresession.AuthorityObserver,
			AckMode:    coresession.AckOnDelivery,
		}),
	)
	if err != nil {
		_ = lease.Close()
		return TurnStart{}, fmt.Errorf("start turn: %w", err)
	}
	broker.BindTurn(turn.RunID(), turn)

	a.mu.Lock()
	a.turns[turn.RunID()] = turn
	a.runConvs[turn.RunID()] = contextID
	if a.convRuns == nil {
		a.convRuns = make(map[string]map[string]bool)
	}
	if a.convRuns[contextID] == nil {
		a.convRuns[contextID] = make(map[string]bool)
	}
	a.convRuns[contextID][turn.RunID()] = true
	a.mu.Unlock()
	go a.waitTurn(lease, turn, contextID)
	return TurnStart{RunID: turn.RunID(), ContextID: contextID}, nil
}

// NewChat starts a fresh conversation: a new session context is
// minted, so subsequent turns keep their own history and permission
// mode. The conversation resets to workspace mode.
func (a *App) NewChat() (string, error) {
	a.mu.Lock()
	a.conversationID = ocsessions.NewID()
	a.mode = ocsessions.ModeWorkspace
	a.think = string(ocsessions.ThinkMedium)
	a.model = ""
	id := a.conversationID
	a.mu.Unlock()
	return id, nil
}

// SessionMode returns the sandbox permission mode of the current
// conversation ("workspace", "read-only", or "yolo").
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
	case ocsessions.ModeWorkspace, ocsessions.ModeReadOnly, ocsessions.ModeYOLO:
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

func (a *App) waitTurn(
	lease *coresession.Lease,
	turn *coresession.Turn,
	contextID string,
) {
	ctx := a.appContext()
	res, err := turn.Wait(ctx)
	runID := turn.RunID()

	a.mu.Lock()
	delete(a.turns, runID)
	delete(a.runConvs, runID)
	turnUsage := a.runUsage[contextID]
	delete(a.runUsage, contextID)
	if a.broker != nil {
		a.broker.UnbindTurn(runID)
	}
	store := a.sessions
	usageStore := a.usage
	wd := a.workDir
	a.mu.Unlock()
	_ = lease.Close()

	if store != nil && turnUsage.TotalTokens > 0 {
		_ = store.RecordUsage(context.Background(), contextID, turnUsage)
	}
	if usageStore != nil && turnUsage.Model != "" {
		_ = usageStore.Record(
			context.Background(),
			workspaceID(wd),
			contextID,
			turnUsage.Model,
			usage.Usage{
				InputTokens:     turnUsage.InputTokens,
				OutputTokens:    turnUsage.OutputTokens,
				CacheReadTokens: turnUsage.CacheReadTokens,
				ReasoningTokens: turnUsage.ReasoningTokens,
				LatencyMs:       turnUsage.LatencyMs,
			},
		)
	}
	// Best-effort auto title: the model summarizes the conversation
	// once; failures keep the first-message fallback. Runs off the UI
	// event path so it never blocks stream delivery.
	go a.autoTitle(context.Background(), contextID)

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
