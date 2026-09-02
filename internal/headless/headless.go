// Package headless runs one agent turn against the fully assembled
// runtime without a GUI or interactive backend: prompts are answered
// with "cancelled" by the Auto backend, so any tool call that needs
// user input fails closed. It backs `opencraft run --json` and the
// Tier-2 end-to-end tests, which drive the same code paths as the
// desktop app.
package headless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	coresession "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/rollout"
	"github.com/GizClaw/opencraft/internal/runtime"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

// Options configures one headless run.
type Options struct {
	WorkDir   string
	ConfigDir string
	Prompt    string
	// Out receives JSONL rollout events (nil disables event output).
	Out io.Writer
}

// Result is the terminal outcome of a headless run.
type Result struct {
	Status         string
	RunID          string
	ConversationID string
	Error          string
	ExitCode       int
}

// Run assembles a runtime for WorkDir, starts one ephemeral session
// with Prompt, and waits for the turn to finish. Stream deltas are
// emitted to Options.Out as JSONL rollout events, so callers get the
// same durable event stream the desktop rollout recorder writes.
func Run(ctx context.Context, opts Options) (Result, error) {
	if strings.TrimSpace(opts.Prompt) == "" {
		return Result{}, errors.New("headless: prompt is required")
	}
	workDir := opts.WorkDir
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return Result{}, fmt.Errorf("headless: workdir: %w", err)
		}
	}
	configDir := opts.ConfigDir
	if configDir == "" {
		var err error
		configDir, err = config.UserConfigDir()
		if err != nil {
			return Result{}, fmt.Errorf("headless: config dir: %w", err)
		}
	}

	mgr, err := config.Open(config.Options{
		WorkDir: workDir,
		UserDir: configDir,
	})
	if err != nil {
		return Result{}, fmt.Errorf("headless: open config: %w", err)
	}
	view, err := mgr.Load(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("headless: load config: %w", err)
	}
	rt, err := app.BuildRuntime(ctx, view.Document,
		app.WithConfigBase(mgr.UserDir()),
		app.WithWorkBase(workDir),
	)
	if err != nil {
		return Result{}, fmt.Errorf("headless: build runtime: %w", err)
	}
	ctrl := runtime.NewController(rt)
	broker := ctrl.Broker(runtime.Auto{})
	if err := broker.Attach(ctx); err != nil {
		_ = ctrl.Close()
		return Result{}, fmt.Errorf("headless: attach broker: %w", err)
	}
	defer func() { _ = ctrl.Close() }()

	var store *ocsessions.Store
	if v, ok := rt.Resource("sessions"); ok {
		if s, ok := v.(*ocsessions.Store); ok {
			store = s
		}
	}
	contextID := ocsessions.NewID()
	if store != nil {
		_ = store.SetMode(ctx, contextID, ocsessions.ModeWorkspace)
	}

	key := coresession.Key{AgentID: "assistant", ContextID: contextID}
	lease, err := rt.Sessions().Open(ctx, key)
	if err != nil {
		return Result{}, fmt.Errorf("headless: open session: %w", err)
	}
	defer func() { _ = lease.Close() }()

	var enc *json.Encoder
	if opts.Out != nil {
		enc = json.NewEncoder(opts.Out)
	}
	rec := &streamRecorder{enc: enc}
	sink := agent.StreamSinkFunc(rec.record)
	turn, err := lease.Session().StartWithOptions(ctx, agent.Request{
		ContextID: contextID,
		Message: message.Message{
			Role:    message.RoleUser,
			Content: message.Content{Parts: []message.Part{message.TextPart{Text: opts.Prompt}}},
		},
		Inputs: map[string]any{
			// Empty think/model: the router applies its default policy,
			// and models without a reasoning capability never receive a
			// reasoning knob (drivers reject it).
			"think_level": "",
			"model":       "",
		},
	},
		coresession.WithEphemeral(),
		coresession.WithSinks(coresession.SinkSpec{
			ID:         "headless",
			Sink:       sink,
			QueueSize:  256,
			Visibility: coresession.VisibilityRaw,
			Authority:  coresession.AuthorityObserver,
			AckMode:    coresession.AckOnDelivery,
		}),
	)
	if err != nil {
		return Result{}, fmt.Errorf("headless: start turn: %w", err)
	}
	runID := turn.RunID()
	rec.runID = runID
	rec.conversID = contextID
	rec.emit(rollout.Event{
		Type:           rollout.TypeTurnStarted,
		ConversationID: contextID,
		RunID:          runID,
	})

	res, waitErr := turn.Wait(ctx)
	result := Result{
		RunID:          runID,
		ConversationID: contextID,
		Status:         "unknown",
	}
	if res != nil {
		result.Status = string(res.Status)
		if res.Err != nil {
			result.Error = res.Err.Error()
		}
	}
	if waitErr != nil && result.Error == "" {
		result.Error = waitErr.Error()
	}
	if result.Error != "" {
		result.Status = "failed"
	}
	if result.Status == "completed" {
		result.ExitCode = 0
	} else {
		result.ExitCode = 1
	}
	typ := rollout.TypeTurnCompleted
	if result.Status != "completed" {
		typ = rollout.TypeTurnFailed
	}
	rec.emit(rollout.Event{
		Type:           typ,
		ConversationID: contextID,
		RunID:          runID,
		Status:         result.Status,
		Error:          result.Error,
	})
	return result, nil
}

// streamRecorder converts stream deltas into rollout JSONL events,
// buffering reasoning/text parts until the stream finish delta.
type streamRecorder struct {
	mu        sync.Mutex
	enc       *json.Encoder
	runID     string
	conversID string
	reasoning strings.Builder
	text      strings.Builder
}

func (r *streamRecorder) record(
	ctx context.Context,
	env event.Envelope,
	delta agent.StreamDeltaPayload,
) error {
	switch delta.Type {
	case agent.StreamDeltaPart:
		switch p := delta.Part.(type) {
		case message.ToolCallPart:
			r.emit(rollout.Event{
				Type:  rollout.TypeItemToolCall,
				RunID: r.runID, ConversationID: r.conversID,
				ItemID: p.Call.ID, Tool: p.Call.Name,
				CallID: p.Call.ID, Arguments: p.Call.Arguments,
			})
		case message.ToolResultPart:
			r.emit(rollout.Event{
				Type:  rollout.TypeItemToolResult,
				RunID: r.runID, ConversationID: r.conversID,
				CallID: p.Result.CallID, Content: p.Result.Content,
				IsError: p.Result.IsError,
			})
		case message.ReasoningPart:
			r.reasoning.WriteString(p.Text)
		case message.TextPart:
			r.text.WriteString(p.Text)
		}
	case agent.StreamDeltaFinish:
		if r.reasoning.Len() > 0 {
			r.emit(rollout.Event{
				Type:  rollout.TypeItemReasoning,
				RunID: r.runID, ConversationID: r.conversID,
				Content: r.reasoning.String(),
			})
			r.reasoning.Reset()
		}
		if r.text.Len() > 0 {
			r.emit(rollout.Event{
				Type:  rollout.TypeItemAssistantMsg,
				RunID: r.runID, ConversationID: r.conversID,
				Content: r.text.String(),
			})
			r.text.Reset()
		}
	}
	return nil
}

func (r *streamRecorder) emit(ev rollout.Event) {
	if r.enc == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ev.Time == "" {
		ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_ = r.enc.Encode(ev)
}
