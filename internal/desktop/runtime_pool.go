package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/agents"
	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
	pluginagent "github.com/GizClaw/opencraft/internal/plugins/agent"
	"github.com/GizClaw/opencraft/internal/rollout"
	"github.com/GizClaw/opencraft/internal/runtime"
	ocsandbox "github.com/GizClaw/opencraft/internal/sandbox"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/undo"
	"github.com/GizClaw/opencraft/internal/usage"
)

// assembledRuntime is the result of assembling one runtime from the
// user configuration layer for one workspace. The caller owns the
// wiring of the artifact observer sink and the lifecycle of ctrl.
type assembledRuntime struct {
	ctrl      *runtime.Controller
	broker    *runtime.Broker
	store     *ocsessions.Store
	lifecycle *agents.Lifecycle
	artifacts *ocsandbox.ArtifactObserver
}

// assembleRuntime loads the user configuration layer for wd, builds a
// runtime with backend and usageObserver, and resolves the session /
// agent / artifact resources. It is shared by the UI rebuild path and
// the background runtime pool.
func (a *App) assembleRuntime(
	ctx context.Context,
	wd string,
	backend runtime.Backend,
	usageObserver func(context.Context, inference.Usage),
) (*assembledRuntime, error) {
	// A discovered project layer is applied only for trusted
	// workspaces. Without the trust gate a third-party repo could
	// silently override hooks (host command execution), sandbox
	// policy, or the execution graph on open.
	opts := config.Options{WorkDir: wd, UserDir: a.userDir}
	if _, present := config.ProjectConfigDir(wd); present && !a.isProjectTrusted(wd) {
		opts.SkipProjectLayer = true
	}
	mgr, err := config.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("desktop: open config: %w", err)
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("desktop: load config: %w", err)
	}
	rt, err := app.BuildRuntime(ctx, view.Document,
		app.WithConfigBase(mgr.UserDir()),
		app.WithWorkBase(wd),
		app.WithUsageObserver(usageObserver),
		app.WithAgentPlugins(pluginagent.NewHost(a.plugins, a.cap)),
		app.WithAutomationHost(&automationHostAdapter{app: a}))
	if err != nil {
		return nil, fmt.Errorf("desktop: assemble runtime: %w", err)
	}
	ctrl := runtime.NewController(rt)
	broker := ctrl.Broker(backend)
	if err := broker.Attach(ctx); err != nil {
		_ = ctrl.Close()
		return nil, fmt.Errorf("desktop: attach broker: %w", err)
	}

	// Prefer the runtime's session store resource: the memory hook
	// and the sandbox share it, so permissions (YOLO), history, and
	// the sessions list all read the same data. A private store is
	// only a fallback for runtimes assembled without the resource.
	var store *ocsessions.Store
	if value, ok := rt.Resource("sessions"); ok {
		if svc, ok := value.(*ocsessions.Store); ok {
			store = svc
		}
	}
	if store == nil {
		userData, err := config.UserDataDir()
		if err != nil {
			broker.Close()
			_ = ctrl.Close()
			return nil, fmt.Errorf("desktop: user data dir: %w", err)
		}
		store, err = ocsessions.New(filepath.Join(userData, "sessions"), 40)
		if err != nil {
			broker.Close()
			_ = ctrl.Close()
			return nil, fmt.Errorf("desktop: session store: %w", err)
		}
	}

	var lifecycle *agents.Lifecycle
	if value, ok := rt.Resource("agentlifecycle"); ok {
		if svc, ok := value.(*agents.Lifecycle); ok {
			lifecycle = svc
		}
	}
	var artifacts *ocsandbox.ArtifactObserver
	if value, ok := rt.Resource("artifacts"); ok {
		if obs, ok := value.(*ocsandbox.ArtifactObserver); ok {
			artifacts = obs
		}
	}
	return &assembledRuntime{
		ctrl:      ctrl,
		broker:    broker,
		store:     store,
		lifecycle: lifecycle,
		artifacts: artifacts,
	}, nil
}

// backgroundHost is one workspace's background runtime: a full runtime
// assembled for task.Workspace, with its own session store, turn
// registry, usage accumulation, and artifact buffering. It never emits
// UI stream/usage/interact events: prompts go through runtime.Auto and
// stream deltas are only recorded into the local rollout.
type backgroundHost struct {
	app     *App
	workDir string

	ctrl     *runtime.Controller
	broker   *runtime.Broker
	sessions *ocsessions.Store
	agents   *agents.Lifecycle

	// All maps below are guarded by app.mu (the pool and the host
	// lifecycle share the app-wide lock with the UI runtime).
	turns           map[string]*coresession.Turn
	runConvs        map[string]string
	runUsage        map[string]ocsessions.Usage
	preTurnSnap     map[string][]undo.FileState
	preTurnManifest map[string]map[string]fileStat
	rollouts        map[string]*rollout.Recorder
	rolloutBufs     map[string]*rolloutBuffer
	// lastOutput accumulates each run's final assistant text so the
	// notification can show the last agent output like a session-end
	// banner does (bounded to avoid unbounded memory).
	lastOutput map[string]string
	titling    map[string]bool
	// stale marks a host whose configuration is outdated: it finishes
	// in-flight runs and is closed, and the next dispatch assembles a
	// fresh host with the latest configuration.
	stale bool
	// closed guards close() against concurrent reap/invalidate calls.
	closed bool
}

// backgroundHostFor returns the live background host for wd, assembling
// one on first use. A stale host is replaced by a fresh assembly.
func (a *App) backgroundHostFor(wd string) (*backgroundHost, error) {
	wd = filepath.Clean(wd)
	a.mu.Lock()
	if h := a.backgroundHosts[wd]; h != nil && !h.stale {
		a.mu.Unlock()
		return h, nil
	}
	a.mu.Unlock()
	return a.assembleBackgroundHost(wd)
}

// assembleBackgroundHost builds a new background host for wd outside
// the app lock. Concurrent assembles for the same workspace are
// resolved by keeping the first one.
func (a *App) assembleBackgroundHost(wd string) (*backgroundHost, error) {
	wd = filepath.Clean(wd)
	info, err := os.Stat(wd)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace %s does not exist", wd)
	}
	host := &backgroundHost{
		app:             a,
		workDir:         wd,
		turns:           make(map[string]*coresession.Turn),
		runConvs:        make(map[string]string),
		runUsage:        make(map[string]ocsessions.Usage),
		preTurnSnap:     make(map[string][]undo.FileState),
		preTurnManifest: make(map[string]map[string]fileStat),
		rollouts:        make(map[string]*rollout.Recorder),
		rolloutBufs:     make(map[string]*rolloutBuffer),
		lastOutput:      make(map[string]string),
		titling:         make(map[string]bool),
	}
	assembled, err := a.assembleRuntime(
		a.appContext(), wd, runtime.Auto{}, host.recordUsage)
	if err != nil {
		return nil, err
	}
	host.ctrl = assembled.ctrl
	host.broker = assembled.broker
	host.sessions = assembled.store
	host.agents = assembled.lifecycle
	if assembled.artifacts != nil {
		assembled.artifacts.SetSink(host.onArtifactWrite)
	}

	a.mu.Lock()
	if existing := a.backgroundHosts[wd]; existing != nil && !existing.stale {
		a.mu.Unlock()
		host.close()
		return existing, nil
	}
	a.backgroundHosts[wd] = host
	a.mu.Unlock()
	return host, nil
}

// invalidateBackgroundHosts applies a configuration change to the
// pool: idle hosts are closed immediately; busy hosts are marked stale
// and closed after their last run ends. In-flight runs are never
// killed.
func (a *App) invalidateBackgroundHosts() {
	a.mu.Lock()
	var idle []*backgroundHost
	for wd, h := range a.backgroundHosts {
		if len(h.turns) == 0 {
			idle = append(idle, h)
			delete(a.backgroundHosts, wd)
		} else {
			h.stale = true
		}
	}
	a.mu.Unlock()
	for _, h := range idle {
		h.close()
	}
}

// reapBackgroundHost closes a stale host once its last turn ended.
func (a *App) reapBackgroundHost(h *backgroundHost) {
	a.mu.Lock()
	if !h.stale || len(h.turns) > 0 {
		a.mu.Unlock()
		return
	}
	if a.backgroundHosts[h.workDir] == h {
		delete(a.backgroundHosts, h.workDir)
	}
	a.mu.Unlock()
	h.close()
}

// closeBackgroundHosts closes every pooled host (app shutdown). Runs
// still in flight are killed by ctrl.Close; their records are
// reconciled as failed on the next launch.
func (a *App) closeBackgroundHosts() {
	a.mu.Lock()
	hosts := make([]*backgroundHost, 0, len(a.backgroundHosts))
	for _, h := range a.backgroundHosts {
		hosts = append(hosts, h)
	}
	a.backgroundHosts = make(map[string]*backgroundHost)
	a.mu.Unlock()
	for _, h := range hosts {
		h.close()
	}
}

// close releases the host's runtime, broker, session store, and
// rollouts. It is idempotent.
func (h *backgroundHost) close() {
	a := h.app
	a.mu.Lock()
	if h.closed {
		a.mu.Unlock()
		return
	}
	h.closed = true
	broker := h.broker
	ctrl := h.ctrl
	store := h.sessions
	rollouts := h.rollouts
	h.broker = nil
	h.ctrl = nil
	h.sessions = nil
	h.agents = nil
	h.turns = make(map[string]*coresession.Turn)
	h.runConvs = make(map[string]string)
	h.runUsage = make(map[string]ocsessions.Usage)
	h.preTurnSnap = make(map[string][]undo.FileState)
	h.preTurnManifest = make(map[string]map[string]fileStat)
	h.rollouts = make(map[string]*rollout.Recorder)
	h.rolloutBufs = make(map[string]*rolloutBuffer)
	h.lastOutput = make(map[string]string)
	h.titling = make(map[string]bool)
	a.mu.Unlock()

	for _, rec := range rollouts {
		_ = rec.Close()
	}
	if broker != nil {
		broker.Close()
	}
	if ctrl != nil {
		_ = ctrl.Close()
	}
	if store != nil {
		_ = store.Close()
	}
}

// runTurn starts one background turn in a fresh conversation context.
// It returns immediately; the terminal TurnEnd is delivered on done.
func (h *backgroundHost) runTurn(
	ctx context.Context,
	prompt string,
	mode ocsessions.Mode,
	think, model string,
	conversationID string,
	done chan<- TurnEnd,
) (TurnStart, error) {
	if strings.TrimSpace(prompt) == "" {
		return TurnStart{}, errors.New("prompt is required")
	}
	a := h.app
	a.mu.Lock()
	ctrl := h.ctrl
	broker := h.broker
	store := h.sessions
	a.mu.Unlock()
	if ctrl == nil || ctrl.Runtime() == nil || broker == nil || store == nil {
		return TurnStart{}, errors.New("后台 runtime 未就绪")
	}

	contextID := conversationID
	if contextID != "" {
		// Run inside an existing session: its own permission mode,
		// thinking level, and model hint apply (all persisted in
		// session.db), and the turn appends to its history.
		if !store.Exists(contextID) {
			return TurnStart{}, fmt.Errorf("session %s not found", contextID)
		}
		thinkLevel, err := store.Think(ctx, contextID)
		if err != nil {
			thinkLevel = ocsessions.ThinkMedium
		}
		modelHint, err := store.Model(ctx, contextID)
		if err != nil {
			modelHint = ""
		}
		think = string(thinkLevel)
		model = modelHint
	} else {
		contextID = ocsessions.NewID()
		if mode == "" {
			mode = ocsessions.ModeWorkspace
		}
		if err := store.SetMode(ctx, contextID, mode); err != nil {
			return TurnStart{}, fmt.Errorf("persist permission mode: %w", err)
		}
		if think != "" {
			if err := store.SetThink(ctx, contextID, ocsessions.ThinkLevel(think)); err != nil {
				return TurnStart{}, fmt.Errorf("persist think level: %w", err)
			}
		}
		if model != "" {
			if err := store.SetModel(ctx, contextID, model); err != nil {
				return TurnStart{}, fmt.Errorf("persist model: %w", err)
			}
		}
	}
	// Only send a reasoning knob when the effective model declares a
	// reasoning capability: drivers reject reasoning_effort for models
	// without one.
	if cfg, err := config.LoadInference(a.userDir); err == nil &&
		!cfg.ModelReasoning(model) {
		think = ""
	}
	before := gitSnapshot(ctx, h.workDir)
	manifest, manifestErr := manifestSnapshot(ctx, h.workDir)

	key := coresession.Key{AgentID: "assistant", ContextID: contextID}
	lease, err := ctrl.Runtime().Sessions().Open(ctx, key)
	if err != nil {
		return TurnStart{}, fmt.Errorf("open session: %w", err)
	}
	turn, err := lease.Session().StartWithOptions(ctx, agent.Request{
		ContextID: contextID,
		Message: message.Message{
			Role: message.RoleUser,
			Content: message.Content{
				Parts: []message.Part{message.TextPart{Text: prompt}},
			},
		},
		Inputs: map[string]any{
			"think_level": think,
			"model":       model,
		},
	},
		coresession.WithEphemeral(),
		coresession.WithSinks(coresession.SinkSpec{
			ID:         "desktop-background",
			Sink:       agent.StreamSinkFunc(h.sink),
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
	h.turns[turn.RunID()] = turn
	h.preTurnSnap[turn.RunID()] = before
	if manifestErr == nil {
		h.preTurnManifest[turn.RunID()] = manifest
	}
	h.runConvs[turn.RunID()] = contextID
	wd := h.workDir
	// Runs in the currently open workspace must be resumable from the
	// main session list while they are still running: the session has
	// no archived history yet, so ResumeSession's store.List lookup
	// would miss it without this in-memory index entry.
	if cur := a.workDir; cur != "" && filepath.Clean(cur) == filepath.Clean(wd) {
		if a.convRuns == nil {
			a.convRuns = make(map[string]map[string]bool)
		}
		if a.convRuns[contextID] == nil {
			a.convRuns[contextID] = make(map[string]bool)
		}
		a.convRuns[contextID][turn.RunID()] = true
	}
	a.mu.Unlock()

	go h.waitTurn(lease, turn, contextID, done)
	// Surface the new session in the main UI immediately when the run
	// targets the currently open workspace: the frontend creates the
	// busy conversation shell and the sidebar shows it as running.
	if a.bridge != nil && a.inCurrentWorkspace(wd) {
		a.bridge.Emit("automation_run_started", map[string]any{
			"run_id":          turn.RunID(),
			"conversation_id": contextID,
		})
	}
	h.recordRollout(ctx, h.rolloutFor(ctx, contextID),
		rollout.Event{
			Type:           rollout.TypeTurnStarted,
			ConversationID: contextID,
			RunID:          turn.RunID(),
		}, "turn started")
	return TurnStart{RunID: turn.RunID(), ContextID: contextID}, nil
}

// sink records background stream deltas into the local rollout and
// routes them into the main UI when the run targets the currently open
// workspace.
func (h *backgroundHost) sink(
	ctx context.Context,
	env event.Envelope,
	delta agent.StreamDeltaPayload,
) error {
	if !agent.IsStreamDelta(env.Subject) {
		return nil
	}
	runID := streamRunID(env.Subject)
	if delta.Type == agent.StreamDeltaPart {
		if part, ok := delta.Part.(message.TextPart); ok {
			a := h.app
			a.mu.Lock()
			out := h.lastOutput[runID] + part.Text
			if len(out) > 8000 {
				out = out[len(out)-8000:]
			}
			h.lastOutput[runID] = out
			a.mu.Unlock()
		}
	}
	h.onStreamRollout(ctx, runID, delta)
	a := h.app
	a.mu.Lock()
	conv := h.runConvs[runID]
	wd := h.workDir
	a.mu.Unlock()
	if a.bridge != nil && conv != "" && a.inCurrentWorkspace(wd) {
		a.bridge.Emit("stream", StreamEvent{
			RunID:          runID,
			ConversationID: conv,
			Delta:          delta,
		})
	}
	return nil
}

// recordUsage accumulates one inference usage report for the owning
// run. It never forwards to the UI bridge.
func (h *backgroundHost) recordUsage(ctx context.Context, u inference.Usage) {
	runID := ""
	if info, ok := agent.RunInfoFromContext(ctx); ok {
		runID = info.RunID
	}
	if runID == "" {
		return
	}
	a := h.app
	a.mu.Lock()
	if h.closed {
		a.mu.Unlock()
		return
	}
	acc := h.runUsage[runID]
	acc.TotalTokens += u.TotalTokens
	acc.InputTokens += u.InputTokens
	acc.OutputTokens += u.OutputTokens
	if u.Model.ID.Provider != "" && u.Model.ID.Name != "" {
		acc.Model = u.Model.ID.Provider + "/" + u.Model.ID.Name
	}
	if u.Output.ReasoningTokens != nil {
		acc.ReasoningTokens += *u.Output.ReasoningTokens
	}
	if u.Input.CacheReadTokens != nil {
		acc.CacheReadTokens += *u.Input.CacheReadTokens
	}
	if u.Input.CacheWriteTokens != nil {
		acc.CacheWriteTokens += *u.Input.CacheWriteTokens
	}
	h.runUsage[runID] = acc
	a.mu.Unlock()
}

// onArtifactWrite buffers an observed workspace write into the
// host's session store without emitting UI artifact events.
func (h *backgroundHost) onArtifactWrite(
	ctx context.Context, path string, data []byte,
) {
	info, ok := agent.RunInfoFromContext(ctx)
	if !ok || info.ConversationID == "" {
		return
	}
	a := h.app
	a.mu.Lock()
	store := h.sessions
	a.mu.Unlock()
	if store != nil {
		_ = store.BufferArtifact(info.ConversationID, path, len(data))
	}
}

// waitTurn waits for one background turn, records usage/rollout,
// reconciles artifacts, and reaps a stale host.
func (h *backgroundHost) waitTurn(
	lease *coresession.Lease,
	turn *coresession.Turn,
	contextID string,
	done chan<- TurnEnd,
) {
	ctx := h.app.appContext()
	res, err := turn.Wait(ctx)
	runID := turn.RunID()

	a := h.app
	a.mu.Lock()
	delete(h.turns, runID)
	delete(h.runConvs, runID)
	turnUsage := h.runUsage[runID]
	delete(h.runUsage, runID)
	output := h.lastOutput[runID]
	delete(h.lastOutput, runID)
	if h.broker != nil {
		h.broker.UnbindTurn(runID)
	}
	store := h.sessions
	usageStore := a.usage
	wd := h.workDir
	manifest, hadManifest := h.preTurnManifest[runID]
	delete(h.preTurnManifest, runID)
	a.mu.Unlock()
	_ = lease.Close()

	end := TurnEnd{
		RunID:          runID,
		ConversationID: contextID,
		Status:         "unknown",
		Output:         strings.TrimSpace(output),
	}
	if res != nil {
		end.Status = string(res.Status)
		if res.Err != nil {
			end.Error = res.Err.Error()
		}
	}
	if err != nil && end.Error == "" {
		end.Error = err.Error()
	}
	typ := rollout.TypeTurnCompleted
	if end.Status == "failed" || end.Error != "" {
		typ = rollout.TypeTurnFailed
	}

	if store != nil && turnUsage.TotalTokens > 0 {
		_ = store.RecordUsage(ctx, contextID, turnUsage)
	}
	if usageStore != nil && turnUsage.Model != "" {
		_ = usageStore.Record(
			ctx,
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
	h.recordTurnEnd(ctx, contextID, runID, typ, end.Status, end.Error, turnUsage)
	if done != nil {
		select {
		case done <- end:
		default:
		}
	}

	// Post-turn manifest reconciliation: merge document files that exec
	// created or modified into the archived turn. Git-free, so
	// non-git workspaces get the same coverage. No artifact_sync UI
	// event is emitted: the automation view shows run history only.
	if hadManifest && manifest != nil {
		if after, err := manifestSnapshot(ctx, wd); err == nil {
			docs := diffDocumentArtifacts(manifest, after)
			if len(docs) > 0 && store != nil {
				_, _ = store.AppendTurnArtifacts(contextID, runID, docs)
			}
		}
	}
	// Best-effort auto title: the model summarizes the conversation
	// once; failures keep the first-message fallback the sessions list
	// already uses.
	go h.autoTitle(ctx, contextID)
	a.reapBackgroundHost(h)
}

// autoTitle generates a short conversation title from the first user
// message once, after a background turn finishes. A manual rename
// always wins. Generation is best-effort.
func (h *backgroundHost) autoTitle(ctx context.Context, contextID string) {
	a := h.app
	a.mu.Lock()
	store := h.sessions
	ctrl := h.ctrl
	if h.titling == nil {
		h.titling = make(map[string]bool)
	}
	if h.titling[contextID] {
		a.mu.Unlock()
		return
	}
	h.titling[contextID] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(h.titling, contextID)
		a.mu.Unlock()
	}()

	if store == nil || ctrl == nil || ctrl.Runtime() == nil {
		return
	}
	var custom string
	if store.ReadState(contextID, "title", &custom) == nil &&
		strings.TrimSpace(custom) != "" {
		return
	}
	first, err := store.FirstUserMessage(contextID)
	if err != nil {
		telemetry.Warn(ctx, "desktop: auto title history load failed",
			otellog.String("session", contextID),
			otellog.String("error", err.Error()))
		return
	}
	first = strings.TrimSpace(first)
	if first == "" {
		telemetry.Warn(ctx, "desktop: auto title skipped, no user message",
			otellog.String("session", contextID))
		return
	}
	value, ok := ctrl.Runtime().Resource("router")
	if !ok {
		telemetry.Warn(ctx, "desktop: auto title skipped, router resource missing",
			otellog.String("session", contextID))
		return
	}
	router, ok := value.(*route.Router)
	if !ok || router == nil {
		telemetry.Warn(ctx, "desktop: auto title skipped, router resource is not a router",
			otellog.String("session", contextID))
		return
	}
	maxTokens := 64
	textIntent := &inference.TextIntent{MaxOutputTokens: &maxTokens}
	if cfg, err := config.LoadInference(a.userDir); err == nil &&
		cfg.ModelReasoning("") {
		reasoning := false
		textIntent.ReasoningEnabled = &reasoning
	}
	response, _, err := router.Generate(ctx, inference.GenerateRequest{
		Context: []message.Message{{
			Role:    message.RoleSystem,
			Content: titleSystemContent(),
		}},
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.Content{Parts: []message.Part{
					message.TextPart{Text: first},
				}},
				Intent: inference.Intent{Text: textIntent},
			},
		},
	})
	if err != nil {
		telemetry.Warn(ctx, "desktop: auto title generation failed",
			otellog.String("session", contextID),
			otellog.String("error", err.Error()))
		return
	}
	title := strings.TrimSpace(response.Message.Content.Text())
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		telemetry.Warn(ctx, "desktop: auto title generation returned empty",
			otellog.String("session", contextID),
			otellog.Int("response_parts", len(response.Message.Content.Parts)),
			otellog.String("finish_reason", string(response.FinishReason)),
			otellog.Int64("output_tokens", response.Usage.OutputTokens))
		return
	}
	const maxTitle = 70
	runes := []rune(title)
	if len(runes) > maxTitle {
		title = string(runes[:maxTitle]) + "…"
	}
	if err := store.WriteState(contextID, "title", title); err == nil {
		if a.bridge != nil {
			a.bridge.Emit("session_updated", map[string]string{"id": contextID})
		}
		telemetry.Info(ctx, "desktop: auto title generated",
			otellog.String("session", contextID),
			otellog.Int("title_chars", len(title)))
	} else {
		telemetry.Warn(ctx, "desktop: auto title write failed",
			otellog.String("session", contextID),
			otellog.String("error", err.Error()))
	}
}

// recordRollout writes one rollout event for the host.
func (h *backgroundHost) recordRollout(
	ctx context.Context,
	rec *rollout.Recorder,
	ev rollout.Event,
	what string,
) {
	if rec == nil {
		return
	}
	if err := rec.Record(ev); err != nil {
		telemetry.Warn(ctx, "rollout: "+what+" write failed",
			otellog.String("conversation", ev.ConversationID),
			otellog.String("run", ev.RunID),
			otellog.String("type", ev.Type),
			otellog.String("error", err.Error()))
	}
}

// rolloutFor lazily opens the conversation's recorder under the host's
// session store.
func (h *backgroundHost) rolloutFor(
	ctx context.Context, conversationID string,
) *rollout.Recorder {
	a := h.app
	a.mu.Lock()
	if h.closed {
		a.mu.Unlock()
		return nil
	}
	if rec, ok := h.rollouts[conversationID]; ok {
		a.mu.Unlock()
		return rec
	}
	store := h.sessions
	a.mu.Unlock()
	if store == nil {
		return nil
	}
	path, err := store.RolloutPath(conversationID)
	if err != nil {
		return nil
	}
	rec, err := rollout.Open(path)
	if err != nil {
		return nil
	}
	a.mu.Lock()
	if existing, ok := h.rollouts[conversationID]; ok {
		a.mu.Unlock()
		_ = rec.Close()
		return existing
	}
	h.rollouts[conversationID] = rec
	a.mu.Unlock()
	h.recordRollout(ctx, rec, rollout.Event{
		Type:           rollout.TypeThreadStarted,
		ConversationID: conversationID,
	}, "thread started")
	return rec
}

// onStreamRollout synthesizes item events from background stream
// deltas (tool calls/results immediately; text/reasoning buffered and
// flushed on the finish delta).
func (h *backgroundHost) onStreamRollout(
	ctx context.Context, runID string, delta agent.StreamDeltaPayload,
) {
	a := h.app
	a.mu.Lock()
	conv := h.runConvs[runID]
	a.mu.Unlock()
	if conv == "" {
		return
	}
	rec := h.rolloutFor(ctx, conv)
	if rec == nil {
		return
	}
	switch delta.Type {
	case agent.StreamDeltaPart:
		switch p := delta.Part.(type) {
		case message.ToolCallPart:
			h.recordRollout(ctx, rec, rollout.Event{
				Type:           rollout.TypeItemToolCall,
				ConversationID: conv,
				RunID:          runID,
				ItemID:         p.Call.ID,
				Tool:           p.Call.Name,
				CallID:         p.Call.ID,
				Arguments:      p.Call.Arguments,
			}, "tool call")
		case message.ToolResultPart:
			h.recordRollout(ctx, rec, rollout.Event{
				Type:           rollout.TypeItemToolResult,
				ConversationID: conv,
				RunID:          runID,
				CallID:         p.Result.CallID,
				Content:        p.Result.Content,
				IsError:        p.Result.IsError,
			}, "tool result")
		case message.ReasoningPart:
			h.rolloutBufferAppend(runID, true, p.Text)
		case message.TextPart:
			h.rolloutBufferAppend(runID, false, p.Text)
		}
	case agent.StreamDeltaFinish:
		buf := h.rolloutBufferTake(runID)
		if buf == nil {
			return
		}
		if buf.reasoning.Len() > 0 {
			h.recordRollout(ctx, rec, rollout.Event{
				Type: rollout.TypeItemReasoning, ConversationID: conv,
				RunID: runID, Content: buf.reasoning.String(),
			}, "reasoning")
		}
		if buf.text.Len() > 0 {
			h.recordRollout(ctx, rec, rollout.Event{
				Type: rollout.TypeItemAssistantMsg, ConversationID: conv,
				RunID: runID, Content: buf.text.String(),
			}, "assistant message")
		}
	}
}

func (h *backgroundHost) rolloutBufferAppend(
	runID string, reasoning bool, text string,
) {
	if text == "" {
		return
	}
	a := h.app
	a.mu.Lock()
	defer a.mu.Unlock()
	buf := h.rolloutBufs[runID]
	if buf == nil {
		buf = &rolloutBuffer{}
		h.rolloutBufs[runID] = buf
	}
	if reasoning {
		buf.reasoning.WriteString(text)
	} else {
		buf.text.WriteString(text)
	}
}

func (h *backgroundHost) rolloutBufferTake(runID string) *rolloutBuffer {
	a := h.app
	a.mu.Lock()
	defer a.mu.Unlock()
	buf := h.rolloutBufs[runID]
	delete(h.rolloutBufs, runID)
	return buf
}

// recordTurnEnd writes turn.completed or turn.failed with usage into
// the host's rollout.
func (h *backgroundHost) recordTurnEnd(
	ctx context.Context,
	conversationID, runID, typ, status, errText string,
	usage ocsessions.Usage,
) {
	rec := h.rolloutFor(ctx, conversationID)
	if rec == nil {
		return
	}
	ev := rollout.Event{
		Type: typ, ConversationID: conversationID, RunID: runID,
		Status: status, Error: errText,
	}
	if usage.TotalTokens > 0 {
		ev.Usage = &rollout.Usage{
			InputTokens:     usage.InputTokens,
			OutputTokens:    usage.OutputTokens,
			CacheReadTokens: usage.CacheReadTokens,
			ReasoningTokens: usage.ReasoningTokens,
			TotalTokens:     usage.TotalTokens,
			LatencyMs:       usage.LatencyMs,
		}
	}
	h.recordRollout(ctx, rec, ev, "turn end")
}
