package bindings

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"
	flowtelemetry "github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/worldstate"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// Conversation exposes chat lifecycle methods over the active Host.
type Conversation struct {
	core *core.Core
}

// NewConversationBinding wires the conversation binding.
func NewConversationBinding(c *core.Core) *Conversation {
	return &Conversation{core: c}
}

// StartTurnRequest starts a user turn in an explicit conversation.
type StartTurnRequest struct {
	ContextID string `json:"context_id"`
	// Workspace names the workspace that owns the conversation. The
	// UI states it explicitly because a staged draft keeps firing
	// after the window moved to another workspace (the Tab queue
	// drains on the terminal event of the turn it waited for), and
	// the turn has to run where its conversation lives. An empty value
	// targets the active workspace, which is what a plain send does.
	Workspace string          `json:"workspace,omitempty"`
	Message   message.Message `json:"message"`
}

// TurnStart reports the run and conversation ids of a started turn.
type TurnStart struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	RequestedAt    string `json:"requested_at,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
}

// StartTurn starts one assistant turn and returns immediately. When
// the current Host retired between the UI action and StartRun, the
// call waits for the replacement Host and retries internally, so the
// frontend never sees the transient lifecycle failure and never
// re-sends the user message.
//
// The turn always runs in the workspace that owns the conversation:
// a conversation the window has left is served by that workspace's
// own (background) Host instead of whatever workspace happens to be
// active. A start whose context id belongs to no workspace the target
// store knows is refused instead of being attached there as a fresh
// session.
func (b *Conversation) StartTurn(
	req StartTurnRequest,
) (TurnStart, error) {
	ctx := b.core.Shell.Context()
	active := b.core.ActiveWorkDir()
	workDir := strings.TrimSpace(req.Workspace)
	if workDir == "" {
		workDir = active
	}
	contextID := req.ContextID
	if contextID == "" {
		contextID = b.core.Conversation.New(workDir)
	} else {
		owner, err := b.resolveConversationWorkspace(
			ctx, workDir, contextID,
		)
		if err != nil {
			return TurnStart{}, err
		}
		if !core.SameWorkspace(owner, workDir) {
			flowtelemetry.Warn(ctx, "conversation: start workspace corrected",
				otellog.String("conversation.id", contextID),
				otellog.String("workspace.stated", workDir),
				otellog.String("workspace.owner", owner))
			workDir = owner
		}
	}
	// A background turn is one whose workspace is not the active one:
	// it is routed to that workspace's Host without becoming current.
	background := workDir != "" && !core.SameWorkspace(workDir, active)
	requestedAt := time.Now().UTC()
	rawSink := agent.StreamSinkFunc(func(
		ctx context.Context,
		env event.Envelope,
		delta agent.StreamDeltaPayload,
	) error {
		if !agent.IsStreamDelta(env.Subject) {
			return nil
		}
		b.core.Shell.EmitStream(core.StreamEvent{
			RunID:          interact.StreamRunID(env.Subject),
			ConversationID: contextID,
			AgentID:        agentIDOrAssistant(env),
			ParentRunID:    env.ParentRunID(),
			Delta:          delta,
		})
		return nil
	})
	// Make this turn's sink resolvable for delegated runs: an async
	// delegation submitted by this turn describes its destination as
	// this conversation, and the worker materializes it back into this
	// sink when the in-process escrow is gone. Registration lives
	// exactly as long as the turn does, and the registry hands back the
	// wrapped, describable sink: the turn has to stream through that
	// same object, or the exporter cannot describe it at all.
	sink, releaseSink := b.core.RegisterConversationSink(contextID, rawSink)
	opts := host.RunOptions{
		Message:   req.Message,
		ContextID: contextID,
		Mode:      b.core.Conversation.Mode(workDir),
		Think:     b.core.Conversation.Think(workDir),
		Model:     b.core.Conversation.Model(workDir),
		Backend:   b.core.Prompt,
		Sink:      sink,
		QueueSize: 256,
		// A person pressed send: a turn they started may preempt a
		// live one on the same conversation (that is what barge-in and
		// steering rely on). Automation turns carry their own origin
		// and step aside instead.
		Origin: host.OriginInteractive,
		OnUsage: func(_ context.Context, usage inference.Usage) {
			b.core.Shell.Emit(core.EventUsage, core.NewUsageEvent(usage))
		},
		// A boundary that takes steered messages reports how many are
		// still waiting: the transcript flips those rows to delivered
		// right away instead of holding them at "waiting for the next
		// step" until the turn ends.
		OnSteerPending: func(_ context.Context, runID string, pending int) {
			b.core.Shell.Emit(core.EventSteerPending, core.SteerPendingEvent{
				RunID:          runID,
				ConversationID: contextID,
				SteerPending:   pending,
			})
		},
	}
	// A Host rebuild can retire the workspace's Host between the
	// frontend send and StartRun. Those lifecycle guards run before any
	// turn side effect, so wait for the replacement Host and retry
	// inside this one RPC instead of surfacing the transient failure.
	//
	// A turn meant for the workspace on screen gives up when the window
	// moves during that wait; a draft draining in a workspace the
	// window has already left keeps its claim across switches, which is
	// the same reason it was routed there in the first place.
	stop := func() bool {
		return !core.SameWorkspace(b.core.ActiveWorkDir(), workDir)
	}
	if background {
		stop = nil
	}
	start := TurnStart{ConversationID: contextID}
	err := b.core.Runtime.Do(
		host.WithAssemblyReason(ctx, host.ReasonConversation),
		workDir, stop,
		func(h *host.Host) error {
			run, err := h.StartRun(ctx, opts)
			if err != nil {
				return err
			}
			startedAt := time.Now().UTC()
			b.core.Conversation.TrackRun(workDir, contextID, run.RunID())
			b.core.Shell.Emit(core.EventStatus, core.StatusEvent{Busy: true})
			start = TurnStart{
				RunID:          run.RunID(),
				ConversationID: contextID,
				RequestedAt:    requestedAt.Format(time.RFC3339),
				StartedAt:      startedAt.Format(time.RFC3339),
			}
			go func() {
				defer releaseSink()
				b.waitTurn(ctx, run, contextID)
			}()
			return nil
		},
	)
	if err != nil {
		// No run ever started, so the sink registration would outlive
		// its reason to exist.
		releaseSink()
		return TurnStart{}, err
	}
	return start, nil
}

// resolveConversationWorkspace returns the workspace that owns one
// conversation. The stores are the only place ownership lives, and a
// start that skipped this check would be attached as a brand-new
// session in the stated workspace — which is how a queued draft aimed
// at another workspace used to resurface as a stray session.
//
// A stale owner hint (the frontend's registry label can lag a
// workspace switch) falls back to the active workspace when that
// store does hold the conversation; a conversation neither store
// knows is refused instead of being minted somewhere.
func (b *Conversation) resolveConversationWorkspace(
	ctx context.Context,
	stated string,
	contextID string,
) (string, error) {
	owned, err := b.workspaceHasConversation(ctx, stated, contextID)
	if err != nil {
		return "", err
	}
	if owned {
		return stated, nil
	}
	if active := b.core.ActiveWorkDir(); !core.SameWorkspace(active, stated) {
		if owned, err := b.workspaceHasConversation(
			ctx, active, contextID,
		); err == nil && owned {
			return active, nil
		}
	}
	return "", fmt.Errorf(
		"conversation: session %q is not in workspace %q",
		contextID, stated)
}

// workspaceHasConversation reports whether one workspace owns a
// conversation, either as its just-minted current conversation (whose
// first turn has nothing persisted yet) or as a stored session.
func (b *Conversation) workspaceHasConversation(
	ctx context.Context,
	workDir string,
	contextID string,
) (bool, error) {
	if strings.TrimSpace(workDir) == "" {
		return false, nil
	}
	if b.core.Conversation.Current(workDir) == contextID {
		return true, nil
	}
	store, release, err := b.workspaceSessions(ctx, workDir)
	if err != nil {
		return false, err
	}
	defer release()
	return store.Exists(contextID), nil
}

// workspaceSessions returns a shared handle on one workspace's
// session store: the live Host's own store when it serves that
// workspace, the manager's pooled store otherwise. The handle is
// released by the returned function.
func (b *Conversation) workspaceSessions(
	ctx context.Context,
	workDir string,
) (*sessions.Store, func(), error) {
	if h := b.core.ActiveHost(); h != nil && h.Sessions() != nil &&
		core.SameWorkspace(h.WorkDir(), workDir) {
		return h.Sessions(), func() {}, nil
	}
	mgr := b.core.Runtime.Manager()
	if mgr == nil {
		return nil, nil, errNotReady("conversation")
	}
	layout, err := b.core.ResolveLayout(workDir)
	if err != nil {
		return nil, nil, err
	}
	store, err := mgr.OpenSessions(ctx, workDir, layout, 40)
	if err != nil {
		return nil, nil, err
	}
	return store, func() { mgr.ReleaseSessions(store) }, nil
}

// waitTurn blocks until the run finishes and emits the terminal
// turn_end event the frontend uses to settle the conversation actor.
// resultErr is the error the engine reported, or nil when the run
// produced a result. It exists so the classification and the rendered
// error text come from the same error.
func resultErr(res *agent.Result) error {
	if res == nil {
		return nil
	}
	return res.Err
}

func (b *Conversation) waitTurn(
	ctx context.Context,
	run *host.Run,
	contextID string,
) {
	res, err := run.Wait(ctx)
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
	finishedAt, durationMs := run.FinishedTiming()
	requestID, responseID := run.FinishedIDs()
	end := core.NewTurnEnd(
		run.RunID(), contextID, status, errText,
		requestID, responseID,
		lastAssistantOutput(res), finishedAt, durationMs, res,
	)
	if end.SteerPending == nil {
		flowtelemetry.Warn(context.WithoutCancel(ctx),
			"conversation: undelivered steer count unreadable; "+
				"the UI keeps every steered row",
			otellog.String("run.id", run.RunID()))
	}
	if res != nil {
		if report, ok := worldstate.CompactionReportFromBoard(res.LastBoard); ok &&
			!report.Empty() {
			end.Compaction = &core.CompactionEvent{
				Folds:    report.Folds,
				Failures: report.Failures,
				Notified: report.Notified,
			}
		}
	}
	class := host.ClassifyRunError(resultErr(res), err)
	end.InterruptCause = class.InterruptCause
	end.ErrorKind = class.ErrorKind
	end.AgentID = core.AssistantAgentID
	b.core.Shell.Emit(core.EventTurnEnd, end)
	b.core.Shell.Emit(core.EventStatus, core.StatusEvent{})
}

// agentIDOrAssistant returns the envelope agent header, falling back to
// the desktop assistant identity when the engine did not stamp one
// (older flowcraft engines or direct message streams).
func agentIDOrAssistant(env event.Envelope) string {
	if id := env.AgentID(); id != "" {
		return id
	}
	return core.AssistantAgentID
}

// lastAssistantOutput returns the bounded text of the final assistant
// message in a run result, or "" when there is none.
func lastAssistantOutput(res *agent.Result) string {
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

// NewChatResult reports a freshly minted conversation with the
// effective session defaults applied at mint time.
type NewChatResult struct {
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
	Think     string `json:"think"`
	Model     string `json:"model"`
}

// NewChat mints a fresh conversation id.
func (b *Conversation) NewChat() NewChatResult {
	workDir := b.core.ActiveWorkDir()
	return NewChatResult{
		SessionID: b.core.Conversation.New(workDir),
		Mode:      string(b.core.Conversation.Mode(workDir)),
		Think:     b.core.Conversation.Think(workDir),
		Model:     b.core.Conversation.Model(workDir),
	}
}

// CurrentSession returns the active conversation id.
func (b *Conversation) CurrentSession() string {
	return b.core.Conversation.Current(b.core.ActiveWorkDir())
}

// SessionMode returns the current conversation sandbox mode.
func (b *Conversation) SessionMode() string {
	return string(b.core.Conversation.Mode(b.core.ActiveWorkDir()))
}

// ResumeSession selects an existing conversation and its settings. A
// conversation that was deleted is refused by name: the store retires
// deleted ids (state.ErrRetired), so the id can come back neither from
// this Host nor from a later one, and focusing it would open a chat
// that can never arrive.
func (b *Conversation) ResumeSession(id string) error {
	ctx := b.core.Shell.Context()
	workDir := b.core.ActiveWorkDir()
	h := b.core.ActiveHost()
	if h == nil || h.Sessions() == nil {
		return fmt.Errorf("conversation: session store is not ready")
	}
	retired, err := h.Sessions().State().Retired(ctx, id)
	if err != nil {
		return err
	}
	if retired {
		return fmt.Errorf("conversation: session %q was deleted", id)
	}
	mode, err := h.Sessions().Mode(ctx, id)
	if err != nil {
		return err
	}
	think, err := h.Sessions().Think(ctx, id)
	if err != nil {
		return err
	}
	model, err := h.Sessions().Model(ctx, id)
	if err != nil {
		return err
	}
	b.core.Conversation.SetCurrent(workDir, id, mode, string(think), model)
	return nil
}

// ForkTurn copies the source conversation through runID into a fresh
// session, switches the UI to it, and returns the new session id.
func (b *Conversation) ForkTurn(
	contextID, runID string,
) (string, error) {
	ctx := b.core.Shell.Context()
	workDir := b.core.ActiveWorkDir()
	h := b.core.ActiveHost()
	if h == nil || h.Sessions() == nil {
		return "", fmt.Errorf("conversation: session store is not ready")
	}
	newID, err := h.ForkConversation(ctx, contextID, runID)
	if err != nil {
		return "", err
	}
	mode, err := h.Sessions().Mode(ctx, newID)
	if err != nil {
		return "", err
	}
	think, err := h.Sessions().Think(ctx, newID)
	if err != nil {
		return "", err
	}
	model, err := h.Sessions().Model(ctx, newID)
	if err != nil {
		return "", err
	}
	b.core.Conversation.SetCurrent(
		workDir, newID, mode, string(think), model,
	)
	return newID, nil
}

// SetSessionMode persists and updates the conversation sandbox mode.
func (b *Conversation) SetSessionMode(mode string) error {
	ctx := b.core.Shell.Context()
	workDir := b.core.ActiveWorkDir()
	m := sessions.Mode(mode)
	switch m {
	case sessions.ModeWorkspace, sessions.ModeReadOnly, sessions.ModeYOLO:
	default:
		return fmt.Errorf("unknown permission mode %q", mode)
	}
	// Defense in depth next to the sessions.Store guard: keep the
	// per-session UI state and the persisted mode on the same page.
	if profile.YoloOnly() && m != sessions.ModeYOLO {
		return fmt.Errorf(
			"conversation: only yolo sandbox mode is available in this build")
	}
	h := b.core.ActiveHost()
	if h != nil && h.Sessions() != nil {
		if err := h.Sessions().SetMode(
			ctx, b.core.Conversation.Current(workDir), m,
		); err != nil {
			return err
		}
	}
	b.core.Conversation.SetMode(workDir, m)
	return nil
}

// CancelTurn cancels one active run.
func (b *Conversation) CancelTurn(runID string) error {
	h, err := b.runHost(b.core.Shell.Context(), runID)
	if err != nil {
		return err
	}
	return h.CancelRun(runID)
}

// Steer hands one mid-turn message to the run a run id identifies: the
// engine delivers it at its next round boundary instead of waiting for
// the turn to end. A rejected message leaves the turn running, so the
// caller keeps its text and decides whether to interrupt, queue it for
// the next turn, or surface the rejection.
func (b *Conversation) Steer(runID, text string) error {
	h, err := b.runHost(b.core.Shell.Context(), runID)
	if err != nil {
		return err
	}
	return h.SteerRun(runID, text)
}

// runHost resolves the Host that owns one live run, for the actions a
// window takes on a run it did not necessarily start in the workspace
// it is showing. A run attributed to a conversation is served by the
// workspace that conversation lives in — the same routing StartTurn
// uses — so a steer or a stop reaches a turn the window moved away from
// instead of dying on whatever Host happens to be on screen. A run this
// process did not attribute (an automation task's run) falls back to
// the window's Host, which is the Host its runner acquired when the
// task's workspace is the one being shown.
func (b *Conversation) runHost(
	ctx context.Context,
	runID string,
) (*host.Host, error) {
	workDir := b.core.Conversation.WorkspaceForRun(runID)
	if workDir == "" {
		if h := b.core.ActiveHost(); h != nil {
			return h, nil
		}
		return nil, errNotReady("conversation")
	}
	return b.core.Runtime.EnsureHost(
		host.WithAssemblyReason(ctx, host.ReasonConversation), workDir)
}

// ReplyPrompt answers one pending interaction.
func (b *Conversation) ReplyPrompt(
	promptID, text, option string,
	options []string, cancel bool,
) (bool, error) {
	return b.core.Prompt.Answer(promptID, text, option, options, cancel), nil
}
