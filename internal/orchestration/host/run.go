package host

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// RunOptions configures one assistant run on a Host.
type RunOptions struct {
	// Message is the user message (text or multimodal parts) that
	// starts this run.
	Message message.Message
	// ContextID reuses an existing conversation when non-empty; empty
	// mints a fresh one.
	ContextID string
	Mode      ocsessions.Mode
	Think     string
	Model     string
	// Sink receives stream deltas; nil disables streaming.
	Sink agent.StreamSink
	// QueueSize bounds the stream sink queue when Sink is set.
	QueueSize int
	// OnUsage receives inference usage attributed to this run.
	OnUsage func(context.Context, inference.Usage)
	// Backend answers interactive prompts for this run. When nil the
	// Host's fallback backend applies, so UI and automation turns can
	// share one runtime with different prompt policies.
	Backend interact.Backend
	// SkipAutoTitle disables the post-run background title generation
	// for one-off callers (for example headless runs) that do not need
	// a title and should not pay an extra model call.
	SkipAutoTitle bool
}

// Run is one live assistant turn owned by a Host.
type Run struct {
	host          *Host
	lease         *coresession.Lease
	turn          *coresession.Turn
	done          bool
	detail        *runDetail
	skipAutoTitle bool
	startedAt     time.Time
	finishedAt    time.Time
	durationMs    int64
}

// ContextID returns the conversation id the run writes to.
func (r *Run) ContextID() string {
	if r == nil || r.detail == nil {
		return ""
	}
	return r.detail.contextID
}

// RunID returns the engine run id.
func (r *Run) RunID() string { return r.turn.RunID() }

// FinishedTiming returns the Host-measured end time and run duration
// in milliseconds once Wait has released the run. Callers that emit a
// terminal turn event should use this instead of re-reading the clock,
// so the event agrees with the persisted turn archive.
func (r *Run) FinishedTiming() (time.Time, int64) {
	if r == nil {
		return time.Time{}, 0
	}
	return r.finishedAt, r.durationMs
}

// StartRun opens a session and starts one assistant turn. The caller
// must call Wait (or Close) to release the lease.
func (h *Host) StartRun(ctx context.Context, opts RunOptions) (*Run, error) {
	if err := validateUserMessage(opts.Message); err != nil {
		return nil, err
	}
	if opts.ContextID != "" && !ocsessions.ValidID(opts.ContextID) {
		return nil, fmt.Errorf("host: invalid session id %q", opts.ContextID)
	}
	ctrl := h.Controller()
	if ctrl == nil || ctrl.Runtime() == nil {
		return nil, errors.New("host: runtime is not ready")
	}
	h.mu.Lock()
	if h.closing || h.closed {
		h.mu.Unlock()
		return nil, errors.New("host: runtime is closing")
	}
	h.mu.Unlock()
	store := h.Sessions()
	if store == nil {
		return nil, errors.New("host: session store is not ready")
	}

	contextID := opts.ContextID
	mode := opts.Mode
	if mode == "" {
		mode = ocsessions.ModeWorkspace
	}
	think := opts.Think
	model := opts.Model
	mint := contextID == ""
	fresh := mint
	if contextID != "" {
		fresh = !store.Exists(contextID)
		if !fresh {
			if m, err := store.Mode(ctx, contextID); err == nil {
				mode = m
			} else {
				telemetry.WarnErr(ctx, "host: load conversation mode failed", err,
					otellog.String("conversation.id", contextID))
			}
			if think == "" {
				if lvl, err := store.Think(ctx, contextID); err == nil {
					think = string(lvl)
				} else {
					telemetry.WarnErr(ctx, "host: load conversation think level failed", err,
						otellog.String("conversation.id", contextID))
				}
			}
			if model == "" {
				if m, err := store.Model(ctx, contextID); err == nil {
					model = m
				} else {
					telemetry.WarnErr(ctx, "host: load conversation model failed", err,
						otellog.String("conversation.id", contextID))
				}
			}
		}
	}
	think = reasoningCapableThink(h.userDir, model, think)
	if fresh {
		if mint {
			contextID = ocsessions.NewID()
		}
		if err := store.SetMode(ctx, contextID, mode); err != nil {
			return nil, fmt.Errorf("host: persist mode: %w", err)
		}
		if think != "" {
			if err := store.SetThink(ctx, contextID, ocsessions.ThinkLevel(think)); err != nil {
				return nil, fmt.Errorf("host: persist think: %w", err)
			}
		}
		if model != "" {
			if err := store.SetModel(ctx, contextID, model); err != nil {
				return nil, fmt.Errorf("host: persist model: %w", err)
			}
		}
	}
	parts, err := persistUserAttachments(
		store, contextID, opts.Message.Content.Parts,
	)
	if err != nil {
		return nil, fmt.Errorf("host: persist attachments: %w", err)
	}
	opts.Message.Content.Parts = parts

	h.fireUserPromptSubmit(ctx, contextID, opts.Message.Content.Text())

	requestedAt := time.Now().UTC()
	manifest, manifestErr := manifestSnapshot(ctx, h.workDir)
	if manifestErr != nil {
		telemetry.WarnErr(ctx, "host: workspace manifest snapshot failed", manifestErr)
		manifest = nil
	}

	key := coresession.Key{AgentID: "assistant", ContextID: contextID}
	lease, err := ctrl.Runtime().Sessions().Open(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("host: open session: %w", err)
	}
	optsList := []coresession.StartOption{coresession.WithEphemeral()}
	if opts.Sink != nil {
		sink := h.observeSink(opts.Sink)
		queue := opts.QueueSize
		if queue <= 0 {
			queue = 256
		}
		optsList = append(optsList, coresession.WithSinks(coresession.SinkSpec{
			ID:         "host",
			Sink:       sink,
			QueueSize:  queue,
			Visibility: coresession.VisibilityRaw,
			Authority:  coresession.AuthorityObserver,
			AckMode:    coresession.AckOnDelivery,
		}))
	}
	inputs := map[string]any{
		"think_level": think,
		"model":       model,
	}
	// The assistant graph attaches provider-side web search through
	// board:llm_extensions. The bag is computed from the inference
	// config (every enabled deployment whose generate models declare
	// hosted web search), not from this turn's model choice: flowcraft
	// strips entries whose deployment id is not selected, so seeding
	// the full bag keeps search available whichever deployment the
	// router picks. Leave the key unset (the graph defaults to an
	// empty bag) when nothing is eligible or config cannot be read.
	if exts := hostedWebSearchExtensions(h.userDir); len(exts) > 0 {
		inputs["llm_extensions"] = exts
	}
	turn, err := lease.Session().StartWithOptions(ctx, agent.Request{
		ContextID: contextID,
		Message:   opts.Message,
		Inputs:    inputs,
	}, optsList...)
	if err != nil {
		telemetry.WarnErr(ctx, "host: close session lease after start failure",
			lease.Close())
		return nil, fmt.Errorf("host: start turn: %w", err)
	}
	if fresh {
		// Persist the first-message fallback title now so a crash
		// before the first archive leaves a titled conversation behind
		// instead of nothing (or a titleless "new session"). Fail the
		// start when the seed itself fails: silently continuing would
		// break the start-persistence guarantee.
		if err := store.SeedStartTitle(
			ctx, contextID, []message.Message{opts.Message},
		); err != nil {
			turn.Cancel()
			telemetry.WarnErr(ctx,
				"host: close session lease after title seed failure",
				lease.Close())
			return nil, fmt.Errorf("host: seed session start title: %w", err)
		}
		h.notifySessionUpdated(ctx, contextID)
	}
	startedAt := time.Now().UTC()
	telemetry.WarnErr(ctx, "host: record turn timing failed",
		store.RecordTurnTiming(
			contextID, turn.RunID(), requestedAt, startedAt,
		))
	if h.Broker() != nil {
		h.Broker().BindTurn(turn.RunID(), turn)
	}
	run := &Run{
		host:          h,
		lease:         lease,
		turn:          turn,
		skipAutoTitle: opts.SkipAutoTitle,
		startedAt:     startedAt,
	}
	run.detail = &runDetail{
		run:       run,
		contextID: contextID,
		notify:    opts.OnUsage,
		manifest:  manifest,
		backend:   opts.Backend,
	}
	h.mu.Lock()
	h.runs[RunID(turn.RunID())] = run.detail
	h.mu.Unlock()
	h.recordRollout(ctx, h.rolloutFor(ctx, contextID), rollout.Event{
		Type:           rollout.TypeTurnStarted,
		ConversationID: contextID,
		RunID:          turn.RunID(),
	}, "turn started")
	return run, nil
}

// reasoningCapableThink drops the reasoning-effort knob when the
// effective model does not declare a reasoning capability. Drivers
// reject reasoning_effort for such models; an empty model hint resolves
// to the router default like the rest of the runtime.
func reasoningCapableThink(userDir, model, think string) string {
	cfg, err := config.LoadInference(userDir)
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"host: load inference config for reasoning capability failed", err)
		return think
	}
	if !cfg.ModelReasoning(model) {
		return ""
	}
	return think
}

// hostedWebSearchExtensions builds the board extension bag for the
// assistant graph from the current inference configuration. A config
// read failure degrades to an empty bag (search stays off) instead of
// failing the turn.
func hostedWebSearchExtensions(userDir string) []config.HostedWebSearchExtension {
	cfg, err := config.LoadInference(userDir)
	if err != nil {
		telemetry.WarnErr(context.Background(),
			"host: load inference config for hosted web search failed", err)
		return nil
	}
	return cfg.WebSearchExtensions()
}

func validateUserMessage(msg message.Message) error {
	if strings.TrimSpace(string(msg.Role)) == "" {
		return errors.New("host: message role is required")
	}
	if msg.Role != message.RoleUser {
		return fmt.Errorf("host: StartRun accepts user messages only, got %q", msg.Role)
	}
	if len(msg.Content.Parts) == 0 {
		return errors.New("host: message content is required")
	}
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("host: message: %w", err)
	}
	return nil
}

// Wait blocks until the turn finishes, unbinds the prompt broker and
// releases the session lease.
func (r *Run) Wait(ctx context.Context) (*agent.Result, error) {
	if r == nil || r.done {
		return nil, errors.New("host: run already finished")
	}
	res, err := r.turn.Wait(ctx)
	r.done = true
	finishedAt := time.Now().UTC()
	if !r.startedAt.IsZero() {
		if duration := finishedAt.Sub(r.startedAt); duration > 0 {
			r.durationMs = duration.Milliseconds()
		}
	}
	r.finishedAt = finishedAt
	host := r.host
	detail := r.detail
	if host != nil && detail != nil {
		turnUsage := host.takeUsage(r.RunID())
		persistCtx := context.WithoutCancel(ctx)
		status := "unknown"
		var errText string
		if res != nil {
			status = string(res.Status)
			if res.Err != nil {
				errText = res.Err.Error()
			}
		}
		if err != nil && errText == "" {
			errText = err.Error()
		}
		execErr := err
		if execErr == nil && res != nil {
			execErr = res.Err
		}
		if execErr != nil {
			telemetry.WarnErr(persistCtx, "host: turn execution failed",
				unwrapErrForTelemetry(execErr),
				otellog.String("conversation.id", detail.contextID),
				otellog.String("run.id", r.RunID()),
				otellog.String("status", status))
		}
		typ := rollout.TypeTurnCompleted
		if errText != "" || status == "failed" {
			typ = rollout.TypeTurnFailed
		}
		store := host.store
		if store != nil {
			telemetry.WarnErr(persistCtx, "host: record turn end failed",
				store.RecordTurnEnd(
					detail.contextID, r.RunID(), finishedAt, status, errText))
		}
		host.persistTurnUsage(persistCtx, detail.contextID, turnUsage)
		host.recordTurnEnd(
			persistCtx, detail.contextID, r.RunID(),
			typ, status, errText, turnUsage)
		if store != nil && detail.manifest != nil {
			if after, snapErr := manifestSnapshot(persistCtx, host.workDir); snapErr == nil {
				docs := diffDocumentArtifacts(detail.manifest, after)
				if len(docs) > 0 {
					_, appendErr := store.AppendTurnArtifacts(
						detail.contextID, r.RunID(), docs)
					telemetry.WarnErr(persistCtx, "host: append turn artifacts failed",
						appendErr)
				}
			}
		}
		if !r.skipAutoTitle {
			host.launchAutoTitle(context.WithoutCancel(ctx), detail.contextID)
		}
		host.dropRun(RunID(r.RunID()))
		host.fireTurnEnd(
			persistCtx, detail.contextID, r.RunID(),
			status, errText, turnUsage)
	}
	if r.host.Broker() != nil {
		r.host.Broker().UnbindTurn(r.RunID())
	}
	if r.lease != nil {
		telemetry.WarnErr(ctx, "host: close run session lease failed",
			r.lease.Close())
	}
	if host != nil {
		host.awaitCloseIfClosing()
	}
	return res, err
}

// persistTurnUsage records one usage delta (a finished turn, an
// auto-title generation, or another post-run model call) in the
// workspace session store and forwards it to the user-level recorder
// installed on the manager. Both writes are best-effort: failures are
// logged and never fail the turn.
func (h *Host) persistTurnUsage(
	ctx context.Context,
	contextID string,
	usage ocsessions.Usage,
) {
	if h == nil || usage.TotalTokens <= 0 {
		return
	}
	if h.store != nil {
		telemetry.WarnErr(ctx, "host: add session usage failed",
			h.store.AddUsage(ctx, contextID, usage))
	}
	h.forwardUsageRecorder(ctx, contextID, usage)
}

// unwrapErrForTelemetry strips one wrapper so telemetry stores the
// underlying error rather than the host/runtime prefix added by the
// Turn execution path.
func unwrapErrForTelemetry(err error) error {
	if err == nil {
		return nil
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		return unwrapped
	}
	return err
}
