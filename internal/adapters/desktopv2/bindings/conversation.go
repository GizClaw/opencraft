package bindings

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/capabilities/undo"
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
}

// StartTurn starts one assistant turn and returns immediately.
func (b *Conversation) StartTurn(
	req StartTurnRequest,
) (TurnStart, error) {
	ctx := b.core.Shell.Context()
	h := b.core.Runtime.Current()
	if h == nil {
		return TurnStart{}, fmt.Errorf("conversation: runtime is not ready")
	}
	contextID := req.ContextID
	if contextID == "" {
		contextID = b.core.Conversation.New()
	}
	st, _ := b.undoStore()
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
		Mode:      b.core.Conversation.Mode(),
		Think:     b.core.Conversation.Think(),
		Model:     b.core.Conversation.Model(),
		Undo:      st,
		Backend:   b.core.Prompt,
		Sink:      sink,
		QueueSize: 256,
	})
	if err != nil {
		return TurnStart{}, err
	}
	b.core.Conversation.TrackRun(contextID, run.RunID())
	go b.waitTurn(ctx, run, contextID)
	return TurnStart{
		RunID:          run.RunID(),
		ConversationID: contextID,
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
	b.core.Shell.Emit("turn_end", map[string]any{
		"run_id":          run.RunID(),
		"conversation_id": contextID,
		"status":          status,
		"error":           errText,
		"finished_at":     time.Now().UTC().Format(time.RFC3339),
	})
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

// NewChat mints a fresh conversation id.
func (b *Conversation) NewChat() string {
	return b.core.Conversation.New()
}

// CurrentSession returns the active conversation id.
func (b *Conversation) CurrentSession() string {
	return b.core.Conversation.Current()
}

// SessionMode returns the current conversation sandbox mode.
func (b *Conversation) SessionMode() string {
	return string(b.core.Conversation.Mode())
}

// ResumeSession selects an existing conversation and its settings.
func (b *Conversation) ResumeSession(id string) error {
	ctx := b.core.Shell.Context()
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
	b.core.Conversation.SetCurrent(id, mode, string(think), model)
	return nil
}

// SetSessionMode persists and updates the conversation sandbox mode.
func (b *Conversation) SetSessionMode(mode string) error {
	ctx := b.core.Shell.Context()
	m := sessions.Mode(mode)
	switch m {
	case sessions.ModeWorkspace, sessions.ModeReadOnly, sessions.ModeYOLO:
	default:
		return fmt.Errorf("unknown permission mode %q", mode)
	}
	h := b.core.Runtime.Current()
	if h != nil && h.Sessions() != nil {
		if err := h.Sessions().SetMode(ctx, b.core.Conversation.Current(), m); err != nil {
			return err
		}
	}
	b.core.Conversation.SetMode(m)
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

func (b *Conversation) undoStore() (*undo.Store, error) {
	workDir := b.core.ActiveWorkDir()
	if workDir == "" {
		return nil, fmt.Errorf("conversation: no workspace selected")
	}
	layout, err := b.core.ResolveLayout(workDir)
	if err != nil {
		return nil, err
	}
	return undo.NewWithRoot(workDir, layout.UndoDir), nil
}

// UndoChange reverts the latest captured turn.
func (b *Conversation) UndoChange() ([]string, error) {
	ctx := b.core.Shell.Context()
	st, err := b.undoStore()
	if err != nil {
		return nil, err
	}
	return st.Undo(ctx, b.core.Conversation.Current())
}

// RedoChange re-applies the latest undone turn.
func (b *Conversation) RedoChange() ([]string, error) {
	ctx := b.core.Shell.Context()
	st, err := b.undoStore()
	if err != nil {
		return nil, err
	}
	return st.Redo(ctx, b.core.Conversation.Current())
}

// UndoState reports undo/redo availability.
func (b *Conversation) UndoState() (undo.State, error) {
	ctx := b.core.Shell.Context()
	st, err := b.undoStore()
	if err != nil {
		return undo.State{}, nil
	}
	return st.Available(ctx, b.core.Conversation.Current())
}

// ReplyPrompt answers one pending interaction.
func (b *Conversation) ReplyPrompt(
	promptID, text, option string,
	options []string, cancel bool,
) (bool, error) {
	return b.core.Prompt.Answer(promptID, text, option, options, cancel), nil
}
