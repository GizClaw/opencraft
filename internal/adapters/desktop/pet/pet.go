// Package pet holds the desktop pet activity projection: it reduces
// UI/automation events into normalized per-agent activity signals and
// derives the surface behavior a pet window should display. It is pure
// desktop observability and must not import the desktop core (which
// assembles it) or any orchestration/capability state.
package pet

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// PetPhase is the normalized activity vocabulary a pet renderer
// understands. The feed reduces high-frequency UI events (stream
// deltas, terminal turn events, interact prompts) into these phases so
// the renderer never has to interpret raw protocol shapes.
type PetPhase string

const (
	PetPhaseIdle      PetPhase = "idle"
	PetPhaseThinking  PetPhase = "thinking"
	PetPhaseTool      PetPhase = "tool"
	PetPhaseAnswering PetPhase = "answering"
	PetPhaseAsking    PetPhase = "asking"
	PetPhaseDone      PetPhase = "done"
	PetPhaseError     PetPhase = "error"
)

// PetToolCategory classifies a tool name for pet behavior selection.
// Keep this vocabulary small: the renderer maps categories to
// animation variants with a wildcard fallback, so adding a category
// here only needs an entry in the classification table.
type PetToolCategory string

const (
	PetToolCategoryExec     PetToolCategory = "exec"
	PetToolCategoryFile     PetToolCategory = "file"
	PetToolCategoryWeb      PetToolCategory = "web"
	PetToolCategoryGenerate PetToolCategory = "generate"
	PetToolCategorySkill    PetToolCategory = "skill"
	PetToolCategoryPlan     PetToolCategory = "plan"
	PetToolCategoryAsk      PetToolCategory = "ask"
	PetToolCategoryDelegate PetToolCategory = "delegate"
	PetToolCategoryOther    PetToolCategory = "other"
)

// PetToolSignal describes the tool a pet is reacting to.
type PetToolSignal struct {
	Name     string          `json:"name"`
	Category PetToolCategory `json:"category"`
}

// PetActivity is the normalized, low-rate signal emitted whenever the
// visible state of one agent changes.
type PetActivity struct {
	AgentID        string         `json:"agent_id"`
	ConversationID string         `json:"conversation_id,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	Phase          PetPhase       `json:"phase"`
	Tool           *PetToolSignal `json:"tool,omitempty"`
	Severity       int            `json:"severity"`
	TS             time.Time      `json:"ts"`
}

func (a PetActivity) clone() PetActivity {
	if a.Tool == nil {
		return a
	}
	tool := *a.Tool
	a.Tool = &tool
	return a
}

// petRunKey tracks one run of one agent. UI runs always carry a run id;
// the fallback to the conversation id keeps terminal events emitted
// without a run id from collapsing into each other.
type petRunKey struct {
	agentID string
	scope   string
}

type petRunState struct {
	activity PetActivity
	updated  time.Time
}

// petRunPruneAfter bounds how long completed/stale runs stay in the
// feed. The director already times visible activity out after seconds,
// but stale high-severity entries (a resolved prompt whose turn never
// re-emitted) must not shadow fresh activity from other runs.
const petRunPruneAfter = 10 * time.Minute

// PetActivityFeed reduces the desktop UI event stream into per-agent
// pet activities. It is pure desktop observability: it never reads
// Host or session state and holds no domain data.
type PetActivityFeed struct {
	mu sync.Mutex

	// now is the clock used for event timestamps. Swappable in tests.
	now          func() time.Time
	defaultAgent string
	runs         map[petRunKey]petRunState
	visible      map[string]PetActivity
}

// NewPetActivityFeed creates an empty feed. Attach it as a Shell pet
// sink so every stream/turn_end/interact event flows through OnEvent.
// defaultAgent names the identity used when an event does not carry an
// agent header (desktop conversation turns are "assistant").
func NewPetActivityFeed(defaultAgent string) *PetActivityFeed {
	return &PetActivityFeed{
		now:          time.Now,
		defaultAgent: defaultAgent,
		runs:         make(map[petRunKey]petRunState),
		visible:      make(map[string]PetActivity),
	}
}

// OnEvent folds one UI event into the feed. It returns the aggregated
// activity when the agent's visible state actually changed, or nil when
// the event was irrelevant or redundant.
func (f *PetActivityFeed) OnEvent(typ string, data any) *PetActivity {
	now := f.now().UTC()

	var agentID, scope string
	var candidate PetActivity
	switch typ {
	case "stream":
		ev := decodeStreamEvent(data)
		if ev == nil {
			return nil
		}
		agentID = f.orAssistant(ev.AgentID)
		scope = ev.RunID
		part := ev.Delta.Part
		switch part.Type {
		case "reasoning":
			if part.Text == "" {
				return nil
			}
			candidate = petActivity(agentID, ev.ConversationID, ev.RunID,
				PetPhaseThinking, nil, now)
		case "tool_call":
			if part.Call.Name == "" {
				return nil
			}
			tool := PetToolSignal{
				Name:     part.Call.Name,
				Category: classifyPetTool(part.Call.Name),
			}
			candidate = petActivity(agentID, ev.ConversationID, ev.RunID,
				PetPhaseTool, &tool, now)
		case "tool_result":
			if !part.Result.IsError {
				return nil
			}
			f.mu.Lock()
			current := f.runs[petRunKey{agentID: agentID, scope: ev.RunID}]
			f.mu.Unlock()
			tool := current.activity.Tool
			candidate = petActivity(agentID, ev.ConversationID, ev.RunID,
				PetPhaseError, tool, now)
		case "text":
			if part.Text == "" {
				return nil
			}
			candidate = petActivity(agentID, ev.ConversationID, ev.RunID,
				PetPhaseAnswering, nil, now)
		default:
			return nil
		}

	case "turn_end":
		ev := decodeTurnEndEvent(data)
		if ev == nil {
			return nil
		}
		agentID = f.orAssistant(ev.AgentID)
		scope = ev.RunID
		if scope == "" {
			scope = "conv:" + ev.ConversationID
		}
		phase := PetPhaseDone
		if ev.Status != "completed" || ev.Error != "" {
			phase = PetPhaseError
		}
		candidate = petActivity(agentID, ev.ConversationID, ev.RunID,
			phase, nil, now)

	case "interact":
		ev := decodeInteractEvent(data)
		if ev == nil {
			return nil
		}
		agentID = f.orAssistant(ev.AgentID)
		scope = ev.RunID
		candidate = petActivity(agentID, ev.ConversationID, ev.RunID,
			PetPhaseAsking, nil, now)

	case "resolved":
		ev := decodeResolvedEvent(data)
		if ev == nil {
			return nil
		}
		agentID = f.orAssistant(ev.AgentID)
		return f.clearAskingLocked(agentID, ev.ConversationID, now)

	default:
		return nil
	}

	if scope == "" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runs[petRunKey{agentID: agentID, scope: scope}] = petRunState{
		activity: candidate,
		updated:  now,
	}
	return f.reaggregateLocked(agentID, now)
}

// Snapshot returns the current visible activity for agentID, or nil
// when the agent has no recorded activity.
func (f *PetActivityFeed) Snapshot(agentID string) *PetActivity {
	f.mu.Lock()
	defer f.mu.Unlock()
	act, ok := f.visible[agentID]
	if !ok || act.Phase == "" {
		return nil
	}
	copy := act.clone()
	return &copy
}

// Activities returns one activity per agent that currently has visible
// state. The Subagent Dock and multi-agent surfaces consume this
// without sharing Director state.
func (f *PetActivityFeed) Activities() []PetActivity {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]PetActivity, 0, len(f.visible))
	for agentID, act := range f.visible {
		if act.Phase == "" {
			continue
		}
		act.AgentID = agentID
		out = append(out, act.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out
}

// LastActivity returns the most recent event time recorded for an
// agent. Unlike Snapshot's timestamp (which only moves when the
// visible state changes), this tracks every event so the director can
// time out long-running phases that never re-emit (for example an
// agent streaming text for a minute).
func (f *PetActivityFeed) LastActivity(agentID string) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	var last time.Time
	for key, state := range f.runs {
		if key.agentID != agentID {
			continue
		}
		if state.updated.After(last) {
			last = state.updated
		}
	}
	return last
}

// clearAskingLocked removes every asking run of one agent inside a
// conversation after the frontend resolves the pending prompt.
func (f *PetActivityFeed) clearAskingLocked(
	agentID, conversationID string, now time.Time,
) *PetActivity {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, state := range f.runs {
		if key.agentID != agentID {
			continue
		}
		if conversationID != "" &&
			state.activity.ConversationID != conversationID {
			continue
		}
		if state.activity.Phase == PetPhaseAsking {
			delete(f.runs, key)
		}
	}
	return f.reaggregateLocked(agentID, now)
}

// reaggregateLocked picks the visible activity for one agent across its
// live runs: higher severity wins; equal severity keeps the most recent
// update.
func (f *PetActivityFeed) reaggregateLocked(
	agentID string, now time.Time,
) *PetActivity {
	f.pruneLocked(now)
	var best petRunState
	bestSet := false
	for key, state := range f.runs {
		if key.agentID != agentID {
			continue
		}
		if !bestSet ||
			state.activity.Severity > best.activity.Severity ||
			(state.activity.Severity == best.activity.Severity &&
				state.updated.After(best.updated)) {
			best = state
			bestSet = true
		}
	}
	if !bestSet {
		delete(f.visible, agentID)
		return nil
	}
	act := best.activity.clone()
	if samePetActivity(f.visible[agentID], act) {
		return nil
	}
	f.visible[agentID] = act
	return &act
}

// pruneLocked drops runs that have not seen an event for longer than
// petRunPruneAfter. Callers hold f.mu.
func (f *PetActivityFeed) pruneLocked(now time.Time) {
	for key, state := range f.runs {
		if now.Sub(state.updated) > petRunPruneAfter {
			delete(f.runs, key)
		}
	}
}

// samePetActivity compares the render-relevant fields of two activities
// (identity, phase and tool). Timestamps and severity are deliberately
// excluded: a repeated signal for the same state must not produce a new
// pet event on every delta.
func samePetActivity(a, b PetActivity) bool {
	if a.AgentID != b.AgentID ||
		a.ConversationID != b.ConversationID ||
		a.RunID != b.RunID ||
		a.Phase != b.Phase {
		return false
	}
	if (a.Tool == nil) != (b.Tool == nil) {
		return false
	}
	if a.Tool == nil {
		return true
	}
	return a.Tool.Name == b.Tool.Name &&
		a.Tool.Category == b.Tool.Category
}

// PetDisposition is the high-level surface behavior the desktop
// window adapter turns into window motion.
type PetDisposition string

const (
	// PetDispositionRoam: no recent agent activity, the pet wanders.
	PetDispositionRoam PetDisposition = "roam"
	// PetDispositionSleep: no activity for a long stretch.
	PetDispositionSleep PetDisposition = "sleep"
	// PetDispositionWork: the agent is producing output (thinking,
	// running tools, answering).
	PetDispositionWork PetDisposition = "work"
	// PetDispositionAsk: the agent is waiting for the user. The window
	// adapter should surface interactivity so the pet can be clicked.
	PetDispositionAsk PetDisposition = "ask"
)

// PetSurfaceState is the snapshot a pet window renders and the window
// adapter moves on.
type PetSurfaceState struct {
	AgentID      string
	Phase        PetPhase
	Tool         *PetToolSignal
	Disposition  PetDisposition
	Interactive  bool
	LastActivity time.Time
	// Intent and Bubble carry one-shot personality reactions decided
	// by the Mind on top of the activity-driven surface state.
	Intent PetIntent
	Bubble string
}

// petIntentHoldFor keeps a one-shot reaction in the broadcast long
// enough for the Rive animation to finish; the longest builtin
// one-shots run about 1.3s and the surface bubble lives 2.2s.
const petIntentHoldFor = 1600 * time.Millisecond

// PetDirector derives surface state from the activity feed. It owns no
// Wails state: window motion and rendering follow its snapshots.
type PetDirector struct {
	feed       *PetActivityFeed
	idleAfter  time.Duration
	sleepAfter time.Duration
	mind       *Mind

	// heldIntent extends a mind one-shot over several rover ticks so
	// the renderer sees one stable intent payload instead of a 60ms
	// blip that would cut the animation short.
	heldIntent PetIntent
	heldUntil  time.Time
}

// NewPetDirector creates a director with the canonical timeout budget:
// an agent idle for 2s goes back to roaming; 90s without events sends
// the pet to sleep.
func NewPetDirector(feed *PetActivityFeed) *PetDirector {
	return &PetDirector{
		feed:       feed,
		idleAfter:  2 * time.Second,
		sleepAfter: 90 * time.Second,
		mind:       NewMind(),
	}
}

// Tick derives the surface state and folds the latest user activity
// pulse into the mind, returning one-shot reactions on top of it.
func (d *PetDirector) Tick(
	agentID string,
	now, lastUserActive time.Time,
	moving bool,
) PetSurfaceState {
	state := d.Poll(agentID, now)
	state = d.mind.Step(state, now, lastUserActive, moving)
	if event := d.mind.NoteUser(now, lastUserActive); event != nil {
		state.Intent = event.Intent
		state.Bubble = event.Bubble
	}
	return d.applyIntentHold(state, now)
}

// applyIntentHold re-emits the current reaction until its animation
// had time to play. A nap is held for as long as the pet stays asleep;
// any fresh intent or activity phase overrides it.
func (d *PetDirector) applyIntentHold(
	state PetSurfaceState,
	now time.Time,
) PetSurfaceState {
	if state.Intent != "" {
		d.heldIntent = state.Intent
		if state.Intent == PetIntentNap {
			d.heldUntil = time.Time{}
		} else {
			d.heldUntil = now.Add(petIntentHoldFor)
		}
		return state
	}
	if state.Phase != PetPhaseIdle {
		d.heldIntent = PetIntentNone
		return state
	}
	switch d.heldIntent {
	case PetIntentNone:
		return state
	case PetIntentNap:
		if state.Disposition != PetDispositionSleep {
			d.heldIntent = PetIntentNone
			return state
		}
		state.Intent = PetIntentNap
	default:
		if now.After(d.heldUntil) {
			d.heldIntent = PetIntentNone
			return state
		}
		state.Intent = d.heldIntent
	}
	return state
}

// NotePoke records a user interaction (click/pet) with this pet.
func (d *PetDirector) NotePoke(now time.Time) {
	d.mind.NotePoke(now)
}

// Debug combines the mind snapshot with the surface state that was
// active when it was produced.
func (d *PetDirector) Debug(state PetSurfaceState) MindDebug {
	debug := d.mind.Debug()
	debug.Disposition = state.Disposition
	debug.Phase = state.Phase
	return debug
}

// Poll derives the current surface state for the assistant agent.
func (d *PetDirector) Poll(agentID string, now time.Time) PetSurfaceState {
	last := d.feed.LastActivity(agentID)
	activity := d.feed.Snapshot(agentID)
	state := PetSurfaceState{
		AgentID:     agentID,
		Phase:       PetPhaseIdle,
		Disposition: PetDispositionRoam,
	}
	if last.IsZero() {
		return state
	}
	state.LastActivity = last

	elapsed := now.Sub(last)
	switch {
	case elapsed >= d.sleepAfter:
		state.Disposition = PetDispositionSleep
		return state
	case elapsed >= d.idleAfter:
		return state
	case activity == nil:
		return state
	}

	state.Phase = activity.Phase
	state.Tool = activity.Tool
	if activity.Phase == PetPhaseAsking {
		state.Disposition = PetDispositionAsk
		state.Interactive = true
		return state
	}
	state.Disposition = PetDispositionWork
	return state
}

func petActivity(
	agentID, conversationID, runID string,
	phase PetPhase, tool *PetToolSignal, now time.Time,
) PetActivity {
	return PetActivity{
		AgentID:        agentID,
		ConversationID: conversationID,
		RunID:          runID,
		Phase:          phase,
		Tool:           tool,
		Severity:       petSeverity(phase),
		TS:             now,
	}
}

func petSeverity(phase PetPhase) int {
	switch phase {
	case PetPhaseAsking, PetPhaseError:
		return 5
	case PetPhaseTool:
		return 4
	case PetPhaseThinking:
		return 3
	case PetPhaseAnswering:
		return 2
	case PetPhaseDone:
		return 1
	default:
		return 0
	}
}

func (f *PetActivityFeed) orAssistant(agentID string) string {
	if agentID == "" {
		return f.defaultAgent
	}
	return agentID
}

// classifyPetTool buckets a tool name into the stable category
// vocabulary. Plugin/MCP tools fall through to Other unless they carry
// a known prefix; the renderer always has a wildcard fallback.
func classifyPetTool(name string) PetToolCategory {
	switch name {
	case "exec_command", "exec_session":
		return PetToolCategoryExec
	case "read_file", "write_file", "list_dir", "grep", "glob",
		"apply_patch", "request_diff", "view_image":
		return PetToolCategoryFile
	case "web_fetch":
		return PetToolCategoryWeb
	case "generate_image", "generate_video":
		return PetToolCategoryGenerate
	case "update_plan":
		return PetToolCategoryPlan
	case "ask_user", "request_permissions":
		return PetToolCategoryAsk
	case "delegate", "delegation_status", "delegation_targets",
		"create_agent", "update_agent", "unregister_agent":
		return PetToolCategoryDelegate
	case "tool_search":
		return PetToolCategorySkill
	}
	if strings.HasPrefix(name, "skill_") {
		return PetToolCategorySkill
	}
	return PetToolCategoryOther
}

// ---- UI event decoders ----

type uiStreamEvent struct {
	AgentID        string `json:"agent_id"`
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
	Delta          struct {
		Part struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Call struct {
				Name string `json:"name"`
			} `json:"call"`
			Result struct {
				IsError bool `json:"is_error"`
			} `json:"result"`
		} `json:"part"`
	} `json:"delta"`
}

func decodeStreamEvent(data any) *uiStreamEvent {
	var ev uiStreamEvent
	if !decodeAny(data, &ev) {
		return nil
	}
	return &ev
}

type uiTurnEndEvent struct {
	AgentID        string `json:"agent_id"`
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	Status         string `json:"status"`
	Error          string `json:"error"`
}

func decodeTurnEndEvent(data any) *uiTurnEndEvent {
	var ev uiTurnEndEvent
	if !decodeAny(data, &ev) {
		return nil
	}
	return &ev
}

type uiInteractEvent struct {
	AgentID        string `json:"agent_id"`
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
}

func decodeInteractEvent(data any) *uiInteractEvent {
	var ev uiInteractEvent
	if !decodeAny(data, &ev) {
		return nil
	}
	return &ev
}

type uiResolvedEvent struct {
	AgentID        string `json:"agent_id"`
	ConversationID string `json:"conversation_id"`
}

func decodeResolvedEvent(data any) *uiResolvedEvent {
	var ev uiResolvedEvent
	if !decodeAny(data, &ev) {
		return nil
	}
	return &ev
}

func decodeAny(data any, dst any) bool {
	if data == nil {
		return false
	}
	b, err := json.Marshal(data)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, dst) == nil
}
