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
	"github.com/GizClaw/opencraft/internal/capabilities/undo"
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
	// Undo enables turn-level git snapshots when set.
	Undo *undo.Store
	// Backend answers interactive prompts for this run. When nil the
	// Host's fallback backend applies, so UI and automation turns can
	// share one runtime with different prompt policies.
	Backend interact.Backend
}

// Run is one live assistant turn owned by a Host.
type Run struct {
	host   *Host
	lease  *coresession.Lease
	turn   *coresession.Turn
	done   bool
	detail *runDetail
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
			}
			if think == "" {
				if lvl, err := store.Think(ctx, contextID); err == nil {
					think = string(lvl)
				}
			}
			if model == "" {
				if m, err := store.Model(ctx, contextID); err == nil {
					model = m
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
	before := gitSnapshot(ctx, h.workDir)
	manifest, manifestErr := manifestSnapshot(ctx, h.workDir)
	if manifestErr != nil {
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
	turn, err := lease.Session().StartWithOptions(ctx, agent.Request{
		ContextID: contextID,
		Message:   opts.Message,
		Inputs: map[string]any{
			"think_level": think,
			"model":       model,
		},
	}, optsList...)
	if err != nil {
		_ = lease.Close()
		return nil, fmt.Errorf("host: start turn: %w", err)
	}
	startedAt := time.Now().UTC()
	_ = store.RecordTurnTiming(
		contextID, turn.RunID(), requestedAt, startedAt,
	)
	if h.Broker() != nil {
		h.Broker().BindTurn(turn.RunID(), turn)
	}
	run := &Run{
		host:  h,
		lease: lease,
		turn:  turn,
	}
	run.detail = &runDetail{
		run:       run,
		contextID: contextID,
		notify:    opts.OnUsage,
		undo:      opts.Undo,
		before:    before,
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
	if cfg, err := config.LoadInference(userDir); err == nil &&
		!cfg.ModelReasoning(model) {
		return ""
	}
	return think
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
		finishedAt := time.Now().UTC()
		typ := rollout.TypeTurnCompleted
		if errText != "" || status == "failed" {
			typ = rollout.TypeTurnFailed
		}
		store := host.store
		if store != nil {
			_ = store.RecordTurnFinished(detail.contextID, r.RunID(), finishedAt)
		}
		if store != nil && turnUsage.TotalTokens > 0 {
			_ = store.RecordUsage(persistCtx, detail.contextID, turnUsage)
		}
		host.recordTurnEnd(
			persistCtx, detail.contextID, r.RunID(),
			typ, status, errText, turnUsage)
		if detail.undo != nil && len(detail.before) > 0 {
			after := gitSnapshot(persistCtx, host.workDir)
			if _, capErr := detail.undo.Capture(
				persistCtx, detail.contextID, detail.before, after,
			); capErr != nil {
				// best-effort: undo capture must not fail the run
				telemetry.Warn(persistCtx, "host: undo capture failed",
					otellog.String("conversation", detail.contextID),
					otellog.String("run", r.RunID()),
					otellog.String("error", capErr.Error()))
			}
		}
		if store != nil && detail.manifest != nil {
			if after, snapErr := manifestSnapshot(persistCtx, host.workDir); snapErr == nil {
				docs := diffDocumentArtifacts(detail.manifest, after)
				if len(docs) > 0 {
					_, _ = store.AppendTurnArtifacts(
						detail.contextID, r.RunID(), docs)
				}
			}
		}
		host.dropRun(RunID(r.RunID()))
		go host.AutoTitle(context.WithoutCancel(ctx), detail.contextID)
		host.fireTurnEnd(
			persistCtx, detail.contextID, r.RunID(),
			status, errText, turnUsage)
	}
	if r.host.Broker() != nil {
		r.host.Broker().UnbindTurn(r.RunID())
	}
	if r.lease != nil {
		_ = r.lease.Close()
	}
	if host != nil {
		host.finishCloseIfIdle()
	}
	return res, err
}

// Close cancels and releases an unfinished run.
func (r *Run) Close() {
	if r == nil || r.done {
		return
	}
	_, _ = r.Wait(context.Background())
}
