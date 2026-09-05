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

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/profile"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
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
	ContextID string          `json:"context_id"`
	Message   message.Message `json:"message"`
}

// TurnStart reports the run and conversation ids of a started turn.
type TurnStart struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	RequestedAt    string `json:"requested_at,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
}

// StartTurn starts one assistant turn and returns immediately.
func (b *Conversation) StartTurn(
	req StartTurnRequest,
) (TurnStart, error) {
	ctx := b.core.Shell.Context()
	workDir := b.core.ActiveWorkDir()
	h := b.core.Runtime.Current()
	if h == nil {
		return TurnStart{}, fmt.Errorf("conversation: runtime is not ready")
	}
	contextID := req.ContextID
	if contextID == "" {
		contextID = b.core.Conversation.New(workDir)
	}
	requestedAt := time.Now().UTC()
	sink := agent.StreamSinkFunc(func(
		ctx context.Context,
		env event.Envelope,
		delta agent.StreamDeltaPayload,
	) error {
		if !agent.IsStreamDelta(env.Subject) {
			return nil
		}
		b.core.Shell.Emit("stream", map[string]any{
			"run_id":          streamRunID(env.Subject),
			"conversation_id": contextID,
			"delta":           delta,
		})
		return nil
	})
	run, err := h.StartRun(ctx, host.RunOptions{
		Message:   req.Message,
		ContextID: contextID,
		Mode:      b.core.Conversation.Mode(workDir),
		Think:     b.core.Conversation.Think(workDir),
		Model:     b.core.Conversation.Model(workDir),
		Backend:   b.core.Prompt,
		Sink:      sink,
		QueueSize: 256,
		OnUsage: func(_ context.Context, usage inference.Usage) {
			b.core.Shell.Emit("usage", core.NewUsageEvent(usage))
		},
	})
	if err != nil {
		return TurnStart{}, err
	}
	startedAt := time.Now().UTC()
	b.core.Conversation.TrackRun(contextID, run.RunID())
	b.core.Shell.Emit("status", core.StatusEvent{Busy: true})
	go b.waitTurn(ctx, run, contextID)
	return TurnStart{
		RunID:          run.RunID(),
		ConversationID: contextID,
		RequestedAt:    requestedAt.Format(time.RFC3339),
		StartedAt:      startedAt.Format(time.RFC3339),
	}, nil
}

// waitTurn blocks until the run finishes and emits the terminal
// turn_end event the frontend uses to settle the conversation actor.
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
	end := core.NewTurnEnd(
		run.RunID(), contextID, status, errText,
		lastAssistantOutput(res), finishedAt, durationMs,
	)
	b.core.Shell.Emit("turn_end", end)
	b.core.Shell.Emit("status", core.StatusEvent{})
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

// streamRunID extracts the run id from a stream subject such as
// "agent.run.<runID>.stream.<actor>.delta".
func streamRunID(subject event.Subject) string {
	parts := strings.Split(string(subject), ".")
	if len(parts) >= 3 &&
		parts[1] == "run" &&
		(parts[0] == "agent" || parts[0] == "engine") {
		return parts[2]
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

// ResumeSession selects an existing conversation and its settings.
func (b *Conversation) ResumeSession(id string) error {
	ctx := b.core.Shell.Context()
	workDir := b.core.ActiveWorkDir()
	h := b.core.Runtime.Current()
	if h == nil || h.Sessions() == nil {
		return fmt.Errorf("conversation: session store is not ready")
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
	h := b.core.Runtime.Current()
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
	h := b.core.Runtime.Current()
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
	h := b.core.Runtime.Current()
	if h == nil {
		return fmt.Errorf("conversation: runtime is not ready")
	}
	return h.CancelRun(runID)
}

// ReplyPrompt answers one pending interaction.
func (b *Conversation) ReplyPrompt(
	promptID, text, option string,
	options []string, cancel bool,
) (bool, error) {
	return b.core.Prompt.Answer(promptID, text, option, options, cancel), nil
}
