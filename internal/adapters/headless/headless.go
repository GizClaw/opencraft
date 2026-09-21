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
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
)

// Options configures one headless run.
type Options struct {
	WorkDir string
	// ConfigDir is the configuration directory; empty falls back to
	// the global ~/.opencraft/config.
	ConfigDir string
	// DataDir is the state root; empty falls back to the config
	// directory's parent, which is the historical single-root layout.
	DataDir string
	// AppHome is the shared content/credential root; empty follows the
	// state root.
	AppHome string
	Prompt  string
	// Quiet suppresses the one-line stderr note that names the state
	// root this run shares (tests and JSONL consumers pass true).
	Quiet bool
	// Out receives JSONL rollout events (nil disables event output).
	Out io.Writer
}

// Result is the terminal outcome of a headless run.
type Result struct {
	Status         agent.Status
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

	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = filepath.Dir(configDir)
	}
	appHome := opts.AppHome
	if appHome == "" {
		appHome = dataDir
	}
	// A headless run shares the state root of whatever else is running
	// (that is the point: it sees the same sessions and user.db) and
	// takes no part in the GUI single-instance lock. Say so out loud —
	// the line is the cheap way to answer "which root did this write
	// to" after the fact.
	if !opts.Quiet {
		_, _ = fmt.Fprintf(os.Stderr,
			"opencraft run: state root %s (app home %s, config %s); "+
				"no GUI single-instance lock\n",
			dataDir, appHome, configDir)
	}
	hostMgr := host.NewManagerAt(dataDir, configDir)
	hostMgr.SetAppHome(appHome)
	hostMgr.SetLeaseKind("headless")
	// Usage accounting is best-effort: the headless run itself must
	// not fail because the user database is unavailable.
	if usageErr := hostMgr.OpenUserDB(ctx); usageErr != nil {
		telemetry.WarnErr(ctx,
			"headless: user usage accounting unavailable; continuing without it",
			usageErr)
	} else {
		defer hostMgr.CloseUserDB()
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
		// One-shot CLI runs return as soon as the turn ends; do not
		// start a background title generation that would add an extra
		// model call or delay teardown.
		SkipAutoTitle: true,
	})
	if err != nil {
		return Result{}, fmt.Errorf("headless: start run: %w", err)
	}
	contextID := run.ContextID()
	runID := run.RunID()
	rec.identify(runID, contextID)
	rec.emit(rollout.Event{
		Type:           rollout.TypeTurnStarted,
		ConversationID: contextID,
		RunID:          runID,
	})

	res, waitErr := run.Wait(ctx)
	result := Result{
		RunID:          runID,
		ConversationID: contextID,
		Status:         agent.Status("unknown"),
	}
	if res != nil {
		result.Status = res.Status
		if res.Err != nil {
			result.Error = res.Err.Error()
		}
	}
	if waitErr != nil && result.Error == "" {
		result.Error = waitErr.Error()
	}
	if result.Error != "" {
		result.Status = agent.StatusFailed
	}
	if result.Status == agent.StatusCompleted {
		result.ExitCode = 0
	} else {
		result.ExitCode = 1
	}
	typ := rollout.TypeTurnCompleted
	if result.Status != agent.StatusCompleted {
		typ = rollout.TypeTurnFailed
	}
	rec.emit(rollout.Event{
		Type:           typ,
		ConversationID: contextID,
		RunID:          runID,
		Status:         string(result.Status),
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
	for _, ev := range rollout.ItemEventsFromStream(
		r.conversID, r.runID, delta,
	) {
		r.encode(ev)
	}
	switch delta.Type {
	case agent.StreamDeltaPart:
		switch p := delta.Part.(type) {
		case message.ReasoningPart:
			r.reasoning.WriteString(p.Text)
		case message.TextPart:
			r.text.WriteString(p.Text)
		}
	case agent.StreamDeltaFinish:
		reasoning, text := r.reasoning.String(), r.text.String()
		r.reasoning.Reset()
		r.text.Reset()
		for _, ev := range rollout.FlushItemEvents(
			r.conversID, r.runID, reasoning, text,
		) {
			r.encode(ev)
		}
	}
	return nil
}

// identify stamps the run identity every emitted event is tagged with.
// The stream sink can deliver deltas before StartRun returns, and record
// reads these fields under the same lock, so the write has to take it
// too: stamping them directly raced the first deltas of a run.
func (r *streamRecorder) identify(runID, contextID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runID = runID
	r.conversID = contextID
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
