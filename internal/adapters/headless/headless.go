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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/capabilities/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/orchestration/migrations"
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

// openUserUsage opens the shared user-level database and attaches the
// usage store. It is best-effort: the headless run itself must not fail
// because usage accounting is unavailable.
func openUserUsage(
	ctx context.Context,
	dataDir string,
) (*db.DB, *usage.Store, error) {
	handle, err := db.Open(filepath.Join(dataDir, "user.db"))
	if err != nil {
		return nil, nil, fmt.Errorf("headless: open user db: %w", err)
	}
	if err := migrations.User(ctx, handle); err != nil {
		telemetry.WarnErr(ctx, "headless: close user db after migration failure",
			handle.Close())
		return nil, nil, fmt.Errorf("headless: migrate user db: %w", err)
	}
	store, err := usage.Attach(handle)
	if err != nil {
		telemetry.WarnErr(ctx, "headless: close user db after usage attach failure",
			handle.Close())
		return nil, nil, fmt.Errorf("headless: attach usage: %w", err)
	}
	return handle, store, nil
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

	dataDir := filepath.Dir(configDir)
	hostMgr := host.NewManagerAt(dataDir, configDir)
	if udb, usageStore, usageErr := openUserUsage(ctx, dataDir); usageErr != nil {
		telemetry.WarnErr(ctx,
			"headless: user usage accounting unavailable; continuing without it",
			usageErr)
	} else {
		hostMgr.SetUsageRecorder(usageStore.RecordSessionUsage)
		defer func() {
			telemetry.WarnErr(context.Background(),
				"headless: close user db failed", udb.Close())
		}()
	}
	h, err := hostMgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		return Result{}, fmt.Errorf("headless: acquire host: %w", err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "headless: close host failed", h.Close())
	}()

	var enc *json.Encoder
	if opts.Out != nil {
		enc = json.NewEncoder(opts.Out)
	}
	rec := &streamRecorder{enc: enc}
	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, opts.Prompt),
		Sink:    agent.StreamSinkFunc(rec.record),
	})
	if err != nil {
		return Result{}, fmt.Errorf("headless: start run: %w", err)
	}
	contextID := run.ContextID()
	runID := run.RunID()
	rec.runID = runID
	rec.conversID = contextID
	rec.emit(rollout.Event{
		Type:           rollout.TypeTurnStarted,
		ConversationID: contextID,
		RunID:          runID,
	})

	res, waitErr := run.Wait(ctx)
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
	r.mu.Lock()
	defer r.mu.Unlock()
	switch delta.Type {
	case agent.StreamDeltaPart:
		switch p := delta.Part.(type) {
		case message.ToolCallPart:
			r.encode(rollout.Event{
				Type:  rollout.TypeItemToolCall,
				RunID: r.runID, ConversationID: r.conversID,
				ItemID: p.Call.ID, Tool: p.Call.Name,
				CallID: p.Call.ID, Arguments: p.Call.Arguments,
			})
		case message.ToolResultPart:
			r.encode(rollout.Event{
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
			r.encode(rollout.Event{
				Type:  rollout.TypeItemReasoning,
				RunID: r.runID, ConversationID: r.conversID,
				Content: r.reasoning.String(),
			})
			r.reasoning.Reset()
		}
		if r.text.Len() > 0 {
			r.encode(rollout.Event{
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.encode(ev)
}

func (r *streamRecorder) encode(ev rollout.Event) {
	if r.enc == nil {
		return
	}
	if ev.Time == "" {
		ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	telemetry.WarnErr(context.Background(),
		"headless: encode rollout event failed", r.enc.Encode(ev))
}
