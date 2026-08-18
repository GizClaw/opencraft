package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"
	sessions "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/runtime"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/skills"
	"github.com/GizClaw/opencraft/internal/tools/applypatch"
	"github.com/GizClaw/opencraft/internal/tui/commands"
)

// Messages.
type turnStartedMsg struct {
	lease *sessions.Lease
	turn  *sessions.Turn
}

type turnDoneMsg struct {
	res *agent.Result
	err error
}

// flushPrintMsg drains the pending output queue into the in-memory
// transcript viewport on the 50ms streaming tick.
type flushPrintMsg struct{}

// pendingCall is one buffered tool-call header waiting for its result.
type pendingCall struct {
	name  string
	lines []string
}

// collapseThreshold is the auto-collapse line count for long blocks.
const collapseThreshold = 8

// collapseHeadTail is how many output lines stay visible at each end of
// a folded tool block.
const collapseHeadTail = 2

// mainPlaceholder is the idle prompt placeholder.
const mainPlaceholder = "Ask OpenCraft…"

// resumePlaceholder is the picker hint shown in /resume mode.
const resumePlaceholder = "↑/↓ pick session · Enter resume · Esc cancel"

// permissionsPlaceholder is the picker hint shown in /permissions mode.
const permissionsPlaceholder = "↑/↓ pick mode · Enter apply · Esc cancel"

// skillsPlaceholder is the picker hint shown in /skills mode.
const skillsPlaceholder = "↑/↓ pick skill · Enter insert $name · Esc cancel"

// mode is the explicit top-level UI state. Keyboard routing and the
// status line derive from it instead of scattered boolean flags.
type mode int

const (
	modeIdle mode = iota
	modeRunning
	modeAnswering
	modeResume
	modePermissions
	modeSkills
	modeTranscript
)

// sessionState is the active turn context; it is nil when idle.
type sessionState struct {
	lease *sessions.Lease
	turn  *sessions.Turn
}

// streamState carries the transcript buffers for the current turn: the
// print queue plus the in-flight text/reasoning/tool lines. The queue
// is drained into the in-memory viewport by flushPending and reset at
// turn boundaries.
type streamState struct {
	pending      []logEntry
	mdBuf        string
	reasoningBuf string
	// msgOpen is true while an assistant text message is streaming;
	// it is closed by the next boundary (tool call, question, turn
	// end) so the message rule is printed below the last paragraph.
	msgOpen      bool
	lastTool     string
	pendingCalls map[string]pendingCall
}

// logKind selects how a log entry renders at the current terminal
// width. Everything width-dependent (wrapping, message rules, the user
// echo box) is rendered from content at display time, so a resize
// reflows the whole transcript instead of leaving stale widths behind.
type logKind int

const (
	// logStyled carries pre-styled logical lines (markdown output, tool
	// blocks, status lines). They wrap to the current width on render.
	logStyled logKind = iota
	// logRule renders one full-width message divider.
	logRule
	// logUser carries the raw user message text, rendered as the
	// composer echo box at the current width.
	logUser
)

// logEntry is one transcript document node.
type logEntry struct {
	kind  logKind
	lines []string // styled logical lines for logStyled
	text  string   // raw message text for logUser
}

// answeringState is live while mode == modeAnswering: the active
// interaction plus anything queued behind it.
type answeringState struct {
	interaction *InteractEvent
	interactQ   []*InteractEvent
	selCursor   int
	selSelected map[int]bool
	selOther    bool
}

func (s *answeringState) reset() {
	s.interaction = nil
	s.interactQ = nil
	s.selCursor = 0
	s.selSelected = make(map[int]bool)
	s.selOther = false
}

// resumeState is live while mode == modeResume: the project session
// picker.
type resumeState struct {
	list   []ocsessions.Meta
	cursor int
}

func (r *resumeState) reset() {
	r.list = nil
	r.cursor = 0
}

// permissionsState is live while mode == modePermissions: the sandbox
// mode picker (workspace | yolo), with an explicit y/Enter
// confirmation required before entering yolo.
type permissionsState struct {
	cursor  int
	confirm bool
}

func (p *permissionsState) reset() {
	p.cursor = 0
	p.confirm = false
}

// skillsState is live while mode == modeSkills: the discovered skill
// picker. Selecting a skill inserts "$name" into the composer; the
// backend (worldstate prepare) is the only injection owner.
type skillsState struct {
	list   []skills.SkillMetadata
	cursor int
}

func (s *skillsState) reset() {
	s.list = nil
	s.cursor = 0
}

// transcriptState is live while mode == modeTranscript: the full
// content of every block that was folded on screen, scrollable above
// the prompt.
type transcriptState struct {
	blocks [][]string
	scroll int
}

// selectionState is an active text selection over the rendered
// transcript, in display coordinates (wrapped lines × cells).
type selectionState struct {
	active         bool
	startY, startX int
	endY, endX     int
}

func (t *transcriptState) append(full []string) {
	t.blocks = append(t.blocks, full)
	// Bound the in-memory transcript so long sessions cannot grow it
	// without limit.
	const maxTranscriptLines = 5000
	total := 0
	for _, b := range t.blocks {
		total += len(b)
	}
	for total > maxTranscriptLines && len(t.blocks) > 1 {
		total -= len(t.blocks[0])
		t.blocks = t.blocks[1:]
	}
}

// Model is the full-screen TUI: agent output streams into an
// in-memory transcript rendered by a scrollable viewport, with the
// status line, composer bar and footer pinned below it. The alternate
// screen is managed by bubbletea, so resizes repaint the whole frame.
type Model struct {
	rtc     *runtime.Controller
	opts    Options
	ctx     context.Context
	program *tea.Program
	bridge  *Bridge
	broker  *runtime.Broker

	mode mode

	// session is the active turn (nil when idle).
	session sessionState

	// stream holds the turn-bounded transcript buffers.
	stream streamState

	// answering is live while mode == modeAnswering.
	answering answeringState

	// resume is live while mode == modeResume.
	resume resumeState

	// permissions is live while mode == modePermissions.
	permissions permissionsState

	// skills is live while mode == modeSkills.
	skills skillsState

	// palette is the inline /-command search while mode == modeIdle.
	palette paletteState

	// mention is the inline $skill completion while mode == modeIdle.
	mention mentionState

	// commandIndex is the BM25 index over the slash command corpus.
	commandIndex *commands.Index

	// prevMode is the mode restored when the transcript overlay
	// closes.
	prevMode mode

	// transcript is live while mode == modeTranscript.
	transcript transcriptState

	// flushArmed tracks the single in-flight flushPrint tick. The
	// pending drain is a self-rescheduling 50ms tick; arming keeps the
	// tick chain from multiplying when messages queue behind a slow
	// render, which would otherwise spiral into a message storm.
	flushArmed bool

	// log is the full in-memory transcript document. Every line that
	// used to stream into the terminal scrollback now lives here, so
	// the user can scroll back through it at any time.
	log []logEntry

	// logVer bumps on every drain; display re-renders when it or the
	// terminal width changes.
	logVer int

	// display is the width-wrapped rendering of log, fed to the
	// viewport. It is the source for selection coordinates and copy.
	display    []string
	displayW   int
	displayVer int

	// logTrimmed is set when trimLog dropped head lines, forcing a
	// full display re-render on the next drain.
	logTrimmed bool

	// selection is the active text selection (drag to select, y to
	// copy, Esc to clear). dragging tracks a live mouse drag.
	selection selectionState
	dragging  bool

	// mouseCapture mirrors the terminal's mouse-reporting state.
	// Bubbletea captures the mouse for transcript drag selection,
	// which disables the terminal's native selection — including the
	// composer input. Ctrl+E toggles capture off so the user can
	// native-select text, and back on for drag selection.
	mouseCapture bool

	// copyFn writes selected text to the clipboard; injectable for
	// tests.
	copyFn func(string) error

	// viewport renders log with wheel/pgup/pgdn scrolling.
	viewport viewport.Model

	// display: model id, token usage and transient status annotation.
	model       string
	version     string
	thinkLevel  string
	usageIn     int64
	usageOut    int64
	usageCacheR int64
	usageCacheW int64
	usageThink  int64
	usageTotal  int64
	usageLat    int64
	usageSeen   bool
	// usageBase is the cumulative token usage of a resumed session,
	// carried across turns so the status line keeps showing it.
	usageBase ocsessions.Usage
	note      string // transient annotation (e.g. "cancelling…")

	// interruptCause is the most recent cooperative interrupt the UI
	// sent the turn (Esc). Prompt resolutions that arrive without a
	// reason use it to label the marker, because the broker never
	// observes an Ask error for a cooperative interrupt.
	interruptCause agent.Cause

	input   textarea.Model
	spinner spinner.Model

	width  int
	height int
}

// New creates the stdout-REPL model over the bridge and interaction
// broker.
func New(
	rtc *runtime.Controller,
	opts Options,
	bridge *Bridge,
	broker *runtime.Broker,
) *Model {
	input := newInput(mainPlaceholder)
	input.Focus()

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = spinnerStyle

	thinkLevel := string(ocsessions.ThinkMedium)
	if opts.Sessions != nil {
		if level, err := opts.Sessions.Think(opts.ContextID); err == nil {
			thinkLevel = string(level)
		}
	}

	return &Model{
		rtc:          rtc,
		opts:         opts,
		ctx:          context.Background(),
		bridge:       bridge,
		broker:       broker,
		model:        opts.Model,
		version:      opts.Version,
		thinkLevel:   thinkLevel,
		input:        input,
		spinner:      spin,
		commandIndex: commands.NewIndex(),
		answering:    answeringState{selSelected: make(map[int]bool)},
		stream:       streamState{pendingCalls: make(map[string]pendingCall)},
		viewport:     viewport.New(0, 0),
		mouseCapture: true,
		copyFn:       clipboard.WriteAll,
	}
}

// setMode transitions between explicit modes, running the exit hook of
// the old mode and the enter hook of the new one.
func (m *Model) setMode(next mode) {
	if m.mode == next {
		return
	}
	m.exitMode(m.mode)
	m.mode = next
	m.enterMode(next)
}

func (m *Model) exitMode(prev mode) {
	switch prev {
	case modeIdle:
		m.palette.reset()
	case modeAnswering:
		m.answering.reset()
	case modeRunning:
		// In-flight text buffers die with the turn; the print queue
		// survives so already-flushed lines still render. The turn
		// handle is cleared before this runs at turn end, so a
		// running -> answering transition keeps the buffers of the
		// still-active turn.
		if m.session.turn == nil {
			m.stream.mdBuf = ""
			m.stream.reasoningBuf = ""
			m.stream.msgOpen = false
		}
	case modeResume:
		m.resume.reset()
	case modePermissions:
		m.permissions.reset()
	case modeSkills:
		m.skills.reset()
	case modeTranscript:
		m.transcript.scroll = 0
	}
}

func (m *Model) enterMode(next mode) {
	switch next {
	case modeIdle, modeRunning:
		m.note = ""
		m.input.Reset()
		m.input.Placeholder = mainPlaceholder
		m.palette.reset()
		m.mention.reset()
	case modeResume:
		m.input.Reset()
		m.input.Placeholder = resumePlaceholder
	case modePermissions:
		m.input.Reset()
		m.input.Placeholder = permissionsPlaceholder
		m.input.Focus()
	case modeSkills:
		m.input.Reset()
		m.input.Placeholder = skillsPlaceholder
		m.input.Focus()
	}
}

// enterTranscript switches to the full-output overlay, remembering the
// mode to restore. It bypasses setMode so the active turn's buffers
// and input survive.
func (m *Model) enterTranscript() {
	if len(m.transcript.blocks) == 0 {
		return
	}
	m.prevMode = m.mode
	m.mode = modeTranscript
	m.transcript.scroll = 0
}

// leaveTranscript restores the mode that was active before the
// overlay opened.
func (m *Model) leaveTranscript() {
	prev := m.prevMode
	m.prevMode = modeIdle
	m.setMode(prev)
}

// newInput builds the prompt textarea: multiline and growing with
// content, with the semantic palette applied.
func newInput(placeholder string) textarea.Model {
	input := textarea.New()
	input.Prompt = "> "
	input.Placeholder = placeholder
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "> "
		}
		return ""
	})
	input.FocusedStyle = textarea.Style{
		Base:        inputTextStyle,
		CursorLine:  inputTextStyle,
		Text:        inputTextStyle,
		Prompt:      composerPromptStyle,
		Placeholder: composerPlaceholderStyle,
	}
	input.BlurredStyle = input.FocusedStyle
	input.SetHeight(1)
	return input
}

func (m *Model) Init() tea.Cmd {
	m.bridge.Start()
	m.queue(startupBanner(m.opts, m.version)...)
	return tea.Batch(textarea.Blink, m.spinner.Tick, m.flushTick())
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Leave one column on each side for the box padding so the
		// composer spans the full terminal width.
		m.input.SetWidth(max(20, msg.Width-2))
		m.resizeInput()
		// Selection coordinates are display-space; after a reflow they
		// no longer point at the same text, so drop the selection.
		m.selection.active = false
		m.dragging = false
		m.syncViewport()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		m.handleMouse(msg)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case batchMsg:
		for _, ev := range msg.events {
			m.applyEvent(ev)
		}
		return m, nil

	case flushPrintMsg:
		m.flushArmed = false
		m.drainPending()
		return m, m.flushTick()

	case turnStartedMsg:
		m.session.lease = msg.lease
		m.session.turn = msg.turn
		m.stream.mdBuf = ""
		m.stream.reasoningBuf = ""
		m.stream.msgOpen = false
		m.stream.pendingCalls = make(map[string]pendingCall)
		m.usageIn, m.usageOut, m.usageThink = 0, 0, 0
		m.usageCacheR, m.usageCacheW = 0, 0
		m.usageTotal, m.usageLat = 0, 0
		// A resumed session carries its baseline into the next turn;
		// a fresh session starts hidden until the first usage report.
		m.usageSeen = m.usageBase != (ocsessions.Usage{})
		if m.broker != nil && msg.turn != nil {
			m.broker.BindTurn(msg.turn.RunID(), msg.turn)
		}
		m.setMode(modeRunning)
		return m, tea.Batch(m.waitCmd(), m.spinner.Tick)

	case turnDoneMsg:
		if m.opts.Sessions != nil {
			usage := m.sessionUsage()
			_ = m.opts.Sessions.RecordUsage(m.ctx, m.opts.ContextID, usage)
			// Carry the session total into the next turn so consecutive
			// turns accumulate instead of each overwriting meta.json
			// with only its own usage.
			m.usageBase = usage
		}
		m.flushMarkdown()
		m.archiveReasoning()
		// Calls whose results never arrived (interrupted/cancelled)
		// still get printed so nothing is lost.
		if len(m.stream.pendingCalls) > 0 {
			for _, c := range m.stream.pendingCalls {
				m.queue(c.lines...)
			}
			m.stream.pendingCalls = make(map[string]pendingCall)
		}
		if msg.err != nil {
			m.appendTurnError(msg.err)
		}
		if msg.res != nil {
			runID := ""
			if m.session.turn != nil {
				runID = m.session.turn.RunID()
			}
			switch msg.res.Status {
			case agent.StatusCanceled:
				m.queue(toolErrStyle.Render("✗ cancelled [" + runID + "]"))
			case agent.StatusInterrupted:
				m.queue(toolErrStyle.Render("✗ interrupted [" + runID + "]"))
			case agent.StatusFailed, agent.StatusAborted:
				err := msg.res.Err
				if err == nil {
					err = fmt.Errorf("turn %s", msg.res.Status)
				}
				m.appendTurnError(err)
			}
		}
		if m.session.lease != nil {
			_ = m.session.lease.Close()
			m.session.lease = nil
		}
		if m.session.turn != nil && m.broker != nil {
			m.broker.UnbindTurn(m.session.turn.RunID())
		}
		m.session.turn = nil
		m.setMode(modeIdle)
		return m, m.flushPending()

	case tea.QuitMsg:
		if m.session.lease != nil {
			_ = m.session.lease.Close()
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) applyEvent(ev Event) {
	switch {
	case ev.Stream != nil:
		m.appendDelta(ev.Stream.Delta)
	case ev.Interact != nil:
		m.enqueueInteraction(ev.Interact)
	case ev.Resolved != nil:
		m.chatResolved(ev.Resolved.ID, ev.Resolved.Status, ev.Resolved.Reason)
	case ev.Status != nil:
		// The status line is derived from the mode; an external
		// status event only annotates it.
		m.note = ev.Status.Text
	case ev.Usage != nil:
		m.usageIn += ev.Usage.InputTokens
		m.usageOut += ev.Usage.OutputTokens
		m.usageCacheR += ev.Usage.CacheReadTokens
		m.usageCacheW += ev.Usage.CacheWriteTokens
		m.usageThink += ev.Usage.ReasoningTokens
		m.usageTotal += ev.Usage.TotalTokens
		m.usageLat += ev.Usage.LatencyMs
		m.usageSeen = true
		if ev.Usage.Model != "" {
			m.model = ev.Usage.Model
		}
	}
}

// appendDelta turns stream parts into pending output lines.
func (m *Model) appendDelta(delta agent.StreamDeltaPayload) {
	if delta.Type != agent.StreamDeltaPart || delta.Part == nil {
		return
	}
	switch p := delta.Part.(type) {
	case message.TextPart:
		m.appendMarkdown(p.Text)
	case message.ReasoningPart:
		m.appendReasoning(p.Text)
	case message.ToolCallPart:
		m.archiveReasoning()
		m.flushMarkdown()
		m.stream.lastTool = p.Call.Name
		lines := renderToolCallHeader(p.Call, m.opts.WorkDir)
		if p.Call.ID != "" {
			// Buffer until the result arrives so parallel calls print
			// as call+result pairs instead of all calls then all
			// results.
			m.stream.pendingCalls[p.Call.ID] = pendingCall{
				name: p.Call.Name, lines: lines,
			}
		} else {
			m.queue(lines...)
		}
	case message.ToolResultPart:
		content := p.Result.Content
		call, hasCall := m.stream.pendingCalls[p.Result.CallID]
		if hasCall {
			delete(m.stream.pendingCalls, p.Result.CallID)
		}
		name := call.name
		if name == "" {
			name = m.stream.lastTool
		}
		body, end, _ := m.resultParts(name, content, p.Result.IsError)
		var header []string
		if hasCall {
			header = call.lines
		}
		m.queue(m.foldBlock(header, body, end)...)
		m.stream.lastTool = ""
	}
}

// resultParts splits a tool result into the indented output body, the
// closing status line and whether the result is ok. Shell tools parse
// their structured JSON; everything else renders as plain text.
func (m *Model) resultParts(
	name, content string, isErr bool,
) (body []string, end string, ok bool) {
	switch name {
	case "exec_command":
		var r struct {
			ExitCode int    `json:"exit_code"`
			Stdout   string `json:"stdout"`
			Stderr   string `json:"stderr"`
		}
		if json.Unmarshal([]byte(content), &r) == nil {
			body = append(body, indentedLines(r.Stderr, toolErrStyle)...)
			body = append(body, indentedLines(r.Stdout, dimStyle)...)
			ok = r.ExitCode == 0
			end = toolOKStyle.Render("  └ ok")
			if !ok {
				end = toolErrStyle.Render(
					fmt.Sprintf("  └ exit %d", r.ExitCode))
			}
			return body, end, ok
		}
	case "read_file":
		var r struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}
		if json.Unmarshal([]byte(content), &r) == nil {
			body = append(body, indentedLines(r.Content, dimStyle)...)
			ok = true
			end = toolOKStyle.Render("  └ " + r.FilePath)
			return body, end, ok
		}
	case "grep":
		var r struct {
			Matches []struct {
				Path       string `json:"path"`
				LineNumber int    `json:"line_number"`
				Line       string `json:"line"`
			} `json:"matches"`
		}
		if json.Unmarshal([]byte(content), &r) == nil {
			for _, m := range r.Matches {
				body = append(body, dimStyle.Render(fmt.Sprintf(
					"  %s:%d: %s", m.Path, m.LineNumber, m.Line)))
			}
			ok = true
			end = toolOKStyle.Render(fmt.Sprintf("  └ %d matches", len(r.Matches)))
			return body, end, ok
		}
	case "exec_session":
		var r struct {
			ExitCode *int   `json:"exit_code"`
			Reason   string `json:"reason"`
			Chunks   []struct {
				Stream string `json:"stream"`
				Data   string `json:"data"`
			} `json:"chunks"`
		}
		if json.Unmarshal([]byte(content), &r) == nil {
			for _, ch := range r.Chunks {
				style := dimStyle
				if ch.Stream == "stderr" {
					style = toolErrStyle
				}
				body = append(body, indentedLines(ch.Data, style)...)
			}
			ok = r.ExitCode == nil || *r.ExitCode == 0
			end = toolOKStyle.Render("  └ ok")
			if !ok {
				end = toolErrStyle.Render(
					fmt.Sprintf("  └ exit %d", *r.ExitCode))
				if r.Reason != "" {
					end = toolErrStyle.Render(
						fmt.Sprintf("  └ exit %d (%s)", *r.ExitCode, r.Reason))
				}
			}
			return body, end, ok
		}
	case "update_plan":
		// The plan checklist was already rendered in the call header;
		// the tool's "Plan updated" text is noise here.
		ok = !isErr
		if isErr {
			end = toolErrStyle.Render("  └ ✗")
		} else {
			end = toolOKStyle.Render("  └ ok")
		}
		return body, end, ok
	case "apply_patch":
		// The numbered diff was already rendered in the call header;
		// on success the result JSON is noise here.
		ok = !isErr
		if isErr {
			body = append(body, indentedLines(content, toolErrStyle)...)
			end = toolErrStyle.Render("  └ ✗")
		} else {
			end = toolOKStyle.Render("  └ ok")
		}
		return body, end, ok
	}
	style := dimStyle
	if isErr {
		style = toolErrStyle
	}
	if lines := jsonLines([]byte(content)); len(lines) > 0 {
		for _, l := range lines {
			body = append(body, style.Render("  "+l))
		}
	} else {
		body = indentedLines(content, style)
	}
	ok = !isErr
	end = toolOKStyle.Render("  └ ok")
	if isErr {
		end = toolErrStyle.Render("  └ ✗")
	}
	return body, end, ok
}

// renderToolCallHeader renders the tool header block. Shell tools read
// like "• Ran <command>"; apply_patch renders a numbered diff; plan
// updates render a checklist; everything else renders its JSON
// arguments as "- key: value" lines under "• <name>".
func renderToolCallHeader(call message.ToolCall, workDir string) []string {
	args := string(call.Arguments)
	var a struct {
		Command     string   `json:"command"`
		Argv        []string `json:"argv"`
		FilePath    string   `json:"file_path"`
		Path        string   `json:"path"`
		Pattern     string   `json:"pattern"`
		Permissions []string `json:"permissions"`
		Explanation string   `json:"explanation"`
		Plan        []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
		Patch string `json:"patch"`
	}
	if json.Unmarshal(call.Arguments, &a) == nil {
		if a.Command != "" {
			args = a.Command
		} else if len(a.Argv) > 0 {
			args = strings.Join(a.Argv, " ")
		}
	}
	args = truncate(args, 200)
	switch call.Name {
	case "apply_patch":
		if lines, ok := renderApplyPatch(a.Patch, workDir); ok {
			return lines
		}
		return []string{toolNameStyle.Render("• apply_patch")}
	case "exec_command", "exec_session":
		return []string{toolNameStyle.Render("• Ran ") + args}
	case "read_file":
		return []string{toolNameStyle.Render("• Read ") + truncate(a.FilePath, 200)}
	case "write_file":
		return []string{toolNameStyle.Render("• Wrote ") + truncate(a.FilePath, 200)}
	case "list_dir":
		where := a.Path
		if where == "" {
			where = "."
		}
		return []string{toolNameStyle.Render("• Listed ") + truncate(where, 200)}
	case "grep":
		where := a.Path
		if where == "" {
			where = "."
		}
		return []string{toolNameStyle.Render(
			"• Grep ") + truncate(a.Pattern, 120) +
			dimStyle.Render(" in "+truncate(where, 80))}
	case "glob":
		return []string{toolNameStyle.Render("• Glob ") + truncate(a.Pattern, 200)}
	case "update_plan":
		if n := len(a.Plan); n > 0 {
			lines := []string{toolNameStyle.Render("• Update plan")}
			for _, item := range a.Plan {
				marker, style := "- [ ]", dimStyle
				switch item.Status {
				case "in_progress":
					marker, style = "- [~]", statusTextStyle
				case "completed":
					marker = "- [x]"
				}
				lines = append(lines, style.Render(fmt.Sprintf(
					"  %s %s (%s)", marker,
					truncate(item.Step, 120), item.Status)))
			}
			if a.Explanation != "" {
				lines = append(lines, dimStyle.Render(
					"  Explanation: "+truncate(a.Explanation, 160)))
			}
			return lines
		}
		return []string{toolNameStyle.Render("• Update plan")}
	case "request_permissions":
		return []string{toolNameStyle.Render(fmt.Sprintf(
			"• Request permissions (%d)", len(a.Permissions)))}
	default:
		lines := []string{toolNameStyle.Render("• " + call.Name)}
		if jl := jsonLines(call.Arguments); len(jl) > 0 {
			for _, l := range jl {
				lines = append(lines, dimStyle.Render("  "+l))
			}
		} else if args != "" && strings.TrimSpace(args) != "{}" {
			lines = append(lines, dimStyle.Render("  "+args))
		}
		return lines
	}
}

// renderApplyPatch renders a codex patch as a numbered, git-style diff
// block. Real file line numbers require workDir (the workspace root);
// without one, or when a file cannot be read, the lines still render
// with hunk-relative numbers. ok is false for empty or unparseable
// patches, and the caller falls back to the plain tool header.
func renderApplyPatch(patch, workDir string) ([]string, bool) {
	if strings.TrimSpace(patch) == "" {
		return nil, false
	}
	diffs, err := applypatch.Diff(patch, diffReader(workDir))
	if err != nil || len(diffs) == 0 {
		return nil, false
	}
	lines := []string{toolNameStyle.Render("• apply_patch")}
	for i, fd := range diffs {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, renderDiffFileHeader(fd))
		lines = append(lines, renderDiffLines(fd.Lines)...)
	}
	return lines, true
}

// diffReader reads workspace files under workDir for line numbering.
func diffReader(workDir string) applypatch.ReadFile {
	if workDir == "" {
		return nil
	}
	return func(path string) (string, error) {
		clean := filepath.Clean(path)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return "", fmt.Errorf(
				"apply_patch: path %q escapes the workspace", path)
		}
		data, err := os.ReadFile(filepath.Join(workDir, clean))
		return string(data), err
	}
}

// renderDiffFileHeader renders one file block header as
// "  path (+N -M)".
func renderDiffFileHeader(fd applypatch.FileDiff) string {
	line := dimStyle.Render("  " + fd.Path + " (")
	line += toolOKStyle.Render("+" + strconv.Itoa(fd.Added))
	if fd.Removed > 0 {
		line += dimStyle.Render(" ") +
			toolErrStyle.Render("-"+strconv.Itoa(fd.Removed))
	}
	return line + dimStyle.Render(")")
}

// renderDiffLines renders one numbered line per diff entry:
//
//	12 │  func x() {
//	13 │- old line
//	13 │+ new line
func renderDiffLines(lines []applypatch.DiffLine) []string {
	if len(lines) == 0 {
		return nil
	}
	maxNum := 1
	for _, l := range lines {
		if l.OldNum > maxNum {
			maxNum = l.OldNum
		}
		if l.NewNum > maxNum {
			maxNum = l.NewNum
		}
	}
	width := len(strconv.Itoa(maxNum))
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		num := l.OldNum
		if l.Kind == applypatch.DiffLineAdd {
			num = l.NewNum
		}
		numStr := ""
		if num > 0 {
			numStr = strconv.Itoa(num)
		}
		text := truncateWidth(l.Text, 160)
		switch l.Kind {
		case applypatch.DiffLineAdd:
			out = append(out, toolOKStyle.Render(fmt.Sprintf(
				"  %*s │+ %s", width, numStr, text)))
		case applypatch.DiffLineDelete:
			out = append(out, toolErrStyle.Render(fmt.Sprintf(
				"  %*s │- %s", width, numStr, text)))
		default:
			out = append(out, dimStyle.Render(fmt.Sprintf(
				"  %*s │  %s", width, numStr, text)))
		}
	}
	return out
}

// jsonLines flattens a JSON payload into display lines: object fields
// become "- key: value", array items become numbered "1- value" lines,
// and nested values indent. It returns nil when data is not JSON.
func jsonLines(data []byte) []string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil
	}
	var out []string
	renderJSONValue(&out, "", v)
	return out
}

func renderJSONValue(out *[]string, indent string, v any) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := t[k]
			if isJSONScalar(val) {
				*out = append(*out, fmt.Sprintf(
					"%s- %s: %s", indent, k, jsonScalarText(val)))
				continue
			}
			*out = append(*out, fmt.Sprintf("%s- %s:", indent, k))
			renderJSONValue(out, indent+"  ", val)
		}
	case []any:
		for i, item := range t {
			if isJSONScalar(item) {
				*out = append(*out, fmt.Sprintf(
					"%s%d- %s", indent, i+1, jsonScalarText(item)))
				continue
			}
			*out = append(*out, fmt.Sprintf("%s%d-", indent, i+1))
			renderJSONValue(out, indent+"  ", item)
		}
	default:
		*out = append(*out, indent+jsonScalarText(v))
	}
}

func isJSONScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

func jsonScalarText(v any) string {
	switch t := v.(type) {
	case string:
		return truncate(t, 120)
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%v", t)
	}
}

// indentedLines prefixes every non-empty line with the block gutter.
func indentedLines(text string, style lipgloss.Style) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, style.Render("  │ "+l))
	}
	return out
}

// foldBlock renders one complete block: an optional header, the output
// body and an optional closing line. Short bodies pass through
// untouched; long bodies keep their first/last lines around an
// ellipsis and record the full block for the Ctrl+T transcript
// overlay. The header and closing line are never folded away.
func (m *Model) foldBlock(header []string, body []string, end string) []string {
	combine := func() []string {
		var out []string
		out = append(out, header...)
		out = append(out, body...)
		if end != "" {
			out = append(out, end)
		}
		return out
	}
	if len(body) <= collapseThreshold {
		return combine()
	}
	m.transcript.append(combine())
	hidden := len(body) - 2*collapseHeadTail
	lines := append([]string{}, header...)
	lines = append(lines, body[:collapseHeadTail]...)
	lines = append(lines, dimStyle.Render(
		fmt.Sprintf("  … +%d lines (Ctrl+T for full output)", hidden)))
	lines = append(lines, body[len(body)-collapseHeadTail:]...)
	if end != "" {
		lines = append(lines, end)
	}
	return lines
}

// appendMarkdown buffers assistant text into paragraphs (split on
// blank lines) and prints each completed paragraph through the
// markdown renderer. A very long paragraph is flushed eagerly so
// streaming stays responsive.
func (m *Model) appendMarkdown(text string) {
	// Reasoning arrives before the answer; archive it so the live
	// panel clears and the Ctrl+T transcript keeps stream order even
	// when the reasoning text carries no newlines.
	m.archiveReasoning()
	// The first text of a message opens its block: a blank line, the
	// white rule and another blank line print above the answer.
	if !m.stream.msgOpen && strings.TrimSpace(text) != "" {
		m.stream.msgOpen = true
		m.queue("")
		m.queueRule()
		m.queue("")
	}
	m.stream.mdBuf += text
	for {
		idx := strings.Index(m.stream.mdBuf, "\n\n")
		if idx < 0 {
			break
		}
		m.printParagraph(m.stream.mdBuf[:idx])
		m.stream.mdBuf = m.stream.mdBuf[idx+2:]
	}
	if len(m.stream.mdBuf) > 4096 {
		m.printParagraph(m.stream.mdBuf)
		m.stream.mdBuf = ""
	}
}

// flushMarkdown prints the remaining paragraph and closes the
// assistant message block (tool call, question, or turn end terminate
// a message).
func (m *Model) flushMarkdown() {
	if strings.TrimSpace(m.stream.mdBuf) != "" {
		m.printParagraph(m.stream.mdBuf)
		m.stream.mdBuf = ""
	}
	if m.stream.msgOpen {
		m.stream.msgOpen = false
		m.queue("")
		m.queueRule()
		m.queue("")
	}
}

// archiveReasoning saves the finished reasoning block to the Ctrl+T
// transcript overlay and clears the live panel. Reasoning no longer
// prints into the scrollback; the gray panel above the composer is the
// live view and the transcript overlay keeps the full text.
func (m *Model) archiveReasoning() {
	if strings.TrimSpace(m.stream.reasoningBuf) != "" {
		m.transcript.append(m.reasoningHistory(
			strings.TrimSpace(m.stream.reasoningBuf)))
		m.stream.reasoningBuf = ""
	}
}

// reasoningHistory renders one archived reasoning block for the
// transcript overlay: a dim label plus the full text wrapped to the
// current width.
func (m *Model) reasoningHistory(text string) []string {
	textW := max(20, m.width-4)
	lines := wrapByWidth(text, textW)
	out := make([]string, 0, len(lines)+1)
	out = append(out, reasoningLabelStyle.Render("reasoning"))
	return append(out, lines...)
}

// reasoningTailHeight is the fixed height of the live reasoning panel:
// the last three wrapped display lines only.
const reasoningTailHeight = 3

// reasoningBox renders the live reasoning tail as a fixed three-line
// gray panel above the composer, or "" when there is no reasoning to
// show. Only the last three wrapped lines are visible; older content
// stays available in the Ctrl+T transcript overlay.
func (m *Model) reasoningBox() string {
	if strings.TrimSpace(m.stream.reasoningBuf) == "" {
		return ""
	}
	textW := max(20, m.width-4)
	lines := wrapByWidth(m.stream.reasoningBuf, textW)
	if len(lines) > reasoningTailHeight {
		lines = lines[len(lines)-reasoningTailHeight:]
		lines[0] = "…" + truncateWidth(lines[0], textW-1)
	}
	for len(lines) < reasoningTailHeight {
		lines = append(lines, " ")
	}
	content := make([]string, len(lines))
	for i, line := range lines {
		content[i] = reasoningPanelText.Render(line)
	}
	return reasoningPanelStyle.Width(max(20, m.width-2)).
		Render(strings.Join(content, "\n"))
}

// printParagraph renders one markdown paragraph into pending lines,
// folding long paragraphs like tool output.
func (m *Model) printParagraph(para string) {
	trimmed := strings.TrimSpace(para)
	if trimmed == "" {
		return
	}
	lines := renderMarkdown(trimmed)
	m.queue(m.foldBlock(nil, lines, "")...)
}

// appendReasoning buffers reasoning text; the whole block is rendered
// as one rounded box when it ends (text, tool call, question, or turn
// end). Very long thoughts flush eagerly in bounded chunks so they
// still stream.
func (m *Model) appendReasoning(text string) {
	// Rare: reasoning after text. Flush the pending paragraph first so
	// the transcript keeps stream order.
	m.flushMarkdown()
	m.stream.reasoningBuf += text
}

// wrapByWidth hard-wraps text into lines whose display width never
// exceeds width. Explicit newlines start a fresh line and reset the
// width counter (interior blank lines are preserved); otherwise it
// breaks mid-token (no space-based word wrap) so long runs, URLs and
// CJK text cannot widen a surrounding box.
func wrapByWidth(text string, width int) []string {
	if width <= 0 {
		width = 20
	}
	var lines []string
	cur := strings.Builder{}
	curW := 0
	for _, r := range text {
		if r == '\n' {
			lines = append(lines, cur.String())
			cur.Reset()
			curW = 0
			continue
		}
		w := lipgloss.Width(string(r))
		if curW > 0 && curW+w > width {
			lines = append(lines, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteRune(r)
		curW += w
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

// flushPending drains every queued line into the in-memory transcript
// viewport immediately. It returns nil: the next frame renders the
// new content. Callers that previously relied on the returned print
// command can keep the same call shape.
func (m *Model) flushPending() tea.Cmd {
	m.drainPending()
	return nil
}

// drainPending moves the queued transcript lines into the in-memory
// log and refreshes the viewport. While the viewport sits at the
// bottom it keeps following the stream; once the user scrolls back it
// stays put so the reading position is never yanked.
func (m *Model) drainPending() {
	if len(m.stream.pending) == 0 {
		return
	}
	// Size the viewport first so the follow math uses the real window
	// (at startup the first drains can arrive before WindowSizeMsg).
	m.syncViewport()
	follow := m.viewport.AtBottom()
	newEntries := m.stream.pending
	m.stream.pending = nil
	m.log = append(m.log, newEntries...)
	m.logVer++
	if follow {
		m.trimLog()
	}
	w := m.currentWidth()
	// Incremental case: nothing was trimmed and the display is already
	// rendered at this width, so only the new entries need wrapping.
	if m.displayVer == m.logVer-1 && m.displayW == w && !m.logTrimmed {
		m.display = append(m.display, m.renderEntries(newEntries, w)...)
		m.displayVer = m.logVer
	} else {
		m.renderDisplay(w)
	}
	m.logTrimmed = false
	m.pushDisplay()
	if follow {
		m.viewport.GotoBottom()
	}
}

// queue appends one styled block of logical lines to the pending
// transcript queue.
func (m *Model) queue(lines ...string) {
	if len(lines) == 0 {
		return
	}
	m.stream.pending = append(m.stream.pending, logEntry{
		kind:  logStyled,
		lines: lines,
	})
}

// queueRule appends a full-width message divider.
func (m *Model) queueRule() {
	m.stream.pending = append(m.stream.pending, logEntry{kind: logRule})
}

// queueUser appends the raw user message, rendered as the composer
// echo box at the current width.
func (m *Model) queueUser(text string) {
	m.stream.pending = append(m.stream.pending, logEntry{
		kind: logUser,
		text: text,
	})
}

// trimLog bounds the in-memory transcript so a very long session
// cannot grow it without limit. Only called while following, so a
// scrolled-up reader never loses lines under their cursor.
func (m *Model) trimLog() {
	const maxLogLines = 10000
	if len(m.log) <= maxLogLines {
		return
	}
	drop := len(m.log) - maxLogLines
	m.log = append([]logEntry(nil), m.log[drop:]...)
	m.logTrimmed = true
}

// currentWidth returns the effective transcript width.
func (m *Model) currentWidth() int {
	if m.width <= 0 {
		return 80
	}
	return m.width
}

// renderDisplay re-renders the whole document at the given width.
func (m *Model) renderDisplay(w int) {
	m.display = m.renderEntries(m.log, w)
	m.displayW = w
	m.displayVer = m.logVer
}

// renderEntries renders log entries into width-wrapped display lines.
func (m *Model) renderEntries(entries []logEntry, w int) []string {
	var out []string
	for _, e := range entries {
		switch e.kind {
		case logRule:
			out = append(out, assistantRuleStyle.Render(
				strings.Repeat("─", max(1, w))))
		case logUser:
			out = append(out, renderEchoBox(e.text, w)...)
		default:
			for _, l := range e.lines {
				out = append(out, wrapStyled(l, w)...)
			}
		}
	}
	return out
}

// wrapStyled wraps one styled logical line to the display width:
// word wrapping for prose, with a hard-wrap fallback so pathological
// tokens (URLs, base64, long code) still break instead of vanishing.
func wrapStyled(line string, w int) []string {
	if w <= 0 || lipgloss.Width(line) <= w {
		return []string{line}
	}
	wrapped := strings.Split(wordwrap.String(line, w), "\n")
	var out []string
	for _, l := range wrapped {
		if lipgloss.Width(l) <= w {
			out = append(out, l)
			continue
		}
		out = append(out, strings.Split(wrap.String(l, w), "\n")...)
	}
	return out
}

// renderEchoBox renders the user message as the composer echo box at
// the current width, exactly like the live composer bar: full-width
// gray background, one-cell padding, "> " prefix on the first line.
func renderEchoBox(text string, w int) []string {
	contentW := max(1, w-2)
	// The "> " / "  " prefix occupies two cells, so the text wraps to
	// contentW-2 and the prefixed line fills the content width exactly.
	textW := max(1, contentW-2)
	lines := strings.Split(wordwrap.String(text, textW), "\n")
	var wrapped []string
	for _, l := range lines {
		if lipgloss.Width(l) <= textW {
			wrapped = append(wrapped, l)
			continue
		}
		wrapped = append(wrapped,
			strings.Split(wrap.String(l, textW), "\n")...)
	}
	lines = wrapped
	for i, l := range lines {
		if i == 0 {
			lines[i] = composerPromptStyle.Render("> ") +
				inputTextStyle.Render(l)
		} else {
			lines[i] = inputTextStyle.Render("  " + l)
		}
		if pad := contentW - lipgloss.Width(lines[i]); pad > 0 {
			lines[i] += inputTextStyle.Render(strings.Repeat(" ", pad))
		}
	}
	return strings.Split(inputBoxStyle.Render(strings.Join(lines, "\n")), "\n")
}

// pushDisplay feeds the (selection-highlighted) display into the
// viewport. The viewport keeps its scroll offset, so dragging a
// selection does not move the window.
func (m *Model) pushDisplay() {
	m.viewport.SetContent(strings.Join(m.highlighted(m.display), "\n"))
}

// transcriptText returns the rendered transcript as plain display
// text (ANSI codes included); used by tests.
func (m *Model) transcriptText() string {
	return strings.Join(m.display, "\n")
}

// highlighted returns the display with the active selection rendered
// in reverse video. Lines outside the selection pass through.
func (m *Model) highlighted(lines []string) []string {
	if !m.selection.active || len(lines) == 0 {
		return lines
	}
	y0, y1 := m.selection.startY, m.selection.endY
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	x0, x1 := m.selection.startX, m.selection.endX
	if m.selection.startY > m.selection.endY {
		x0, x1 = x1, x0
	}
	out := make([]string, len(lines))
	copy(out, lines)
	if y1 >= len(lines) {
		y1 = len(lines) - 1
	}
	for y := y0; y <= y1 && y < len(lines); y++ {
		c0, c1 := 0, lipgloss.Width(lines[y])
		if y == y0 {
			c0 = x0
		}
		if y == y1 {
			c1 = x1
		}
		if c1 <= c0 {
			continue
		}
		out[y] = highlightRange(lines[y], c0, c1)
	}
	return out
}

// highlightRange reverses the display cells [c0, c1) of one styled
// line, keeping the surrounding styles intact.
func highlightRange(line string, c0, c1 int) string {
	width := lipgloss.Width(line)
	if c1 > width {
		c1 = width
	}
	if c0 >= c1 {
		return line
	}
	pre := ansi.Cut(line, 0, c0)
	sel := ansi.Cut(line, c0, c1)
	post := ansi.Cut(line, c1, width)
	return pre + selectionStyle.Render(sel) + post
}

// selectedText returns the plain text covered by the selection.
func (m *Model) selectedText() string {
	if !m.selection.active {
		return ""
	}
	y0, y1 := m.selection.startY, m.selection.endY
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	x0, x1 := m.selection.startX, m.selection.endX
	if m.selection.startY > m.selection.endY {
		x0, x1 = x1, x0
	}
	var b strings.Builder
	for y := y0; y <= y1 && y < len(m.display); y++ {
		c0, c1 := 0, lipgloss.Width(m.display[y])
		if y == y0 {
			c0 = x0
		}
		if y == y1 {
			c1 = x1
		}
		part := ansi.Cut(m.display[y], c0, c1)
		b.WriteString(strings.TrimRight(ansi.Strip(part), " "))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// clearSelection drops an active selection and repaints the viewport
// without the highlight. It is used after a copy, on Esc, and when a
// left click lands outside the transcript (terminal convention:
// clicking elsewhere cancels the selection).
func (m *Model) clearSelection() {
	m.selection.active = false
	m.dragging = false
	m.pushDisplay()
}

// handleMouse routes wheel events to the viewport and drives drag
// selection inside the transcript area.
func (m *Model) handleMouse(msg tea.MouseMsg) {
	if !m.mouseCapture {
		return
	}
	if m.mode == modeTranscript {
		return
	}
	if tea.MouseEvent(msg).IsWheel() {
		vp, _ := m.viewport.Update(msg)
		m.viewport = vp
		return
	}
	// Selection only makes sense inside the transcript viewport rows.
	// A left click on the composer, pickers or footer drops any active
	// selection, so copy mode cannot hijack the keys typed below (the
	// composer's first "y" in particular).
	if m.viewport.Height <= 0 || msg.Y < 0 || msg.Y >= m.viewport.Height {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.clearSelection()
		}
		return
	}
	y := m.viewport.YOffset + msg.Y
	if y >= len(m.display) {
		y = len(m.display) - 1
	}
	if y < 0 {
		return
	}
	x := msg.X
	if x < 0 {
		x = 0
	}
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft {
			m.selection.active = true
			m.selection.startY, m.selection.startX = y, x
			m.selection.endY, m.selection.endX = y, x
			m.dragging = true
			m.pushDisplay()
		}
	case tea.MouseActionMotion:
		if m.dragging && m.selection.active {
			m.selection.endY, m.selection.endX = y, x
			m.pushDisplay()
		}
	case tea.MouseActionRelease:
		m.dragging = false
		if m.selection.active {
			m.pushDisplay()
		}
	}
}

// toggleMouseCapture flips the terminal mouse-reporting state. Capture
// off restores the terminal's native selection (so the user can select
// text in the composer); capture on re-enables transcript drag
// selection.
func (m *Model) toggleMouseCapture() tea.Cmd {
	m.mouseCapture = !m.mouseCapture
	if m.mouseCapture {
		return tea.EnableMouseCellMotion
	}
	m.clearSelection()
	return tea.DisableMouse
}

// syncViewport sizes the viewport to the current layout and refreshes
// its content when new lines drained. It is idempotent and cheap when
// nothing changed, so it can run on every resize and drain.
func (m *Model) syncViewport() {
	w := m.currentWidth()
	h := m.viewportHeight()
	if h < 1 {
		h = 1
	}
	if w != m.viewport.Width {
		m.viewport.Width = w
	}
	if h != m.viewport.Height {
		m.viewport.Height = h
	}
	if m.displayVer != m.logVer || m.displayW != w {
		m.renderDisplay(w)
		m.pushDisplay()
	}
	// Clamp any scroll offset that is stale after a height change or a
	// content trim.
	m.viewport.SetYOffset(m.viewport.YOffset)
}

// viewportHeight returns the number of rows the transcript viewport
// gets: the terminal height minus the pinned bottom stack (status,
// reasoning panel, composer or picker, footer and trailing blank).
// The transcript overlay mode replaces the viewport entirely, so its
// height is irrelevant and reported as zero.
func (m *Model) viewportHeight() int {
	h := m.height
	if h <= 0 {
		h = 24
	}
	switch m.mode {
	case modeTranscript:
		return 0
	}
	bottom := 2 // footer + trailing blank
	if m.statusLine() != "" {
		bottom++
	}
	if box := m.reasoningBox(); box != "" {
		bottom += len(strings.Split(box, "\n"))
	}
	switch m.mode {
	case modeResume:
		bottom += len(strings.Split(m.resumePicker(), "\n"))
	case modePermissions:
		bottom += len(strings.Split(m.permissionsPicker(), "\n"))
	case modeSkills:
		bottom += len(strings.Split(m.skillsPicker(), "\n"))
	case modeAnswering:
		ev := m.answering.interaction
		if ev != nil && !m.answering.selOther &&
			(ev.Spec.Kind == runtime.KindSelect ||
				ev.Spec.Kind == runtime.KindConfirm) {
			bottom += len(strings.Split(m.interactionSelector(), "\n"))
		} else {
			// The composer bar carries one row of vertical padding on
			// each side.
			bottom += m.input.Height() + 2
		}
	default:
		if m.paletteOpen() {
			bottom += len(strings.Split(m.paletteView(), "\n"))
		} else if m.mentionOpen() {
			bottom += len(strings.Split(m.mentionView(), "\n"))
		}
		bottom += m.input.Height() + 2
	}
	return max(1, h-bottom)
}

func (m *Model) flushTick() tea.Cmd {
	if m.flushArmed {
		return nil
	}
	m.flushArmed = true
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
		return flushPrintMsg{}
	})
}

// dispatch applies one domain event without re-arming the wait loop.
// It exists for tests; the event loop uses applyEvent directly.
func (m *Model) dispatch(ev Event) (tea.Model, tea.Cmd) {
	m.applyEvent(ev)
	return m, nil
}

// ---------- interactions ----------

func (m *Model) enqueueInteraction(ev *InteractEvent) {
	m.archiveReasoning()
	m.flushMarkdown()
	m.queue(reasoningLabelStyle.Render("? " + ev.Spec.Title))
	m.answering.interactQ = append(m.answering.interactQ, ev)
	if m.answering.interaction == nil {
		m.promoteInteraction()
	}
}

func (m *Model) promoteInteraction() {
	if len(m.answering.interactQ) == 0 {
		if m.mode == modeAnswering {
			m.setMode(modeRunning)
		}
		return
	}
	ev := m.answering.interactQ[0]
	m.answering.interactQ = m.answering.interactQ[1:]
	if ev.Spec.Kind == runtime.KindConfirm && len(ev.Spec.Options) == 0 {
		ev.Spec.Options = []runtime.Option{
			{Label: "Yes", Value: "yes"},
			{Label: "No", Value: "no"},
		}
	}
	m.answering.interaction = ev
	m.answering.selCursor = 0
	m.answering.selSelected = make(map[int]bool)
	m.answering.selOther = false
	m.input.Reset()
	m.input.Placeholder = interactionPlaceholder(ev.Spec)
	m.setMode(modeAnswering)
	m.input.Focus()
}

func interactionPlaceholder(spec runtime.Spec) string {
	switch spec.Kind {
	case runtime.KindText:
		return "Type your answer… (Enter to send, Esc to cancel)"
	case runtime.KindConfirm:
		return "↑/↓ or y/n choose · Enter confirm · Esc cancel"
	default:
		return "↑/↓ choose · Space multi-pick · Enter confirm · Esc cancel"
	}
}

func (m *Model) chatResolved(id string, status sessions.PromptStatus, reason string) {
	if m.answering.interaction != nil && m.answering.interaction.Spec.ID == id {
		m.queue(dimStyle.Render("✗ " + m.resolutionLabel(status, reason)))
		m.answering.interaction = nil
		m.promoteInteraction()
		return
	}
	for i, ev := range m.answering.interactQ {
		if ev.Spec.ID == id {
			m.answering.interactQ = append(
				m.answering.interactQ[:i], m.answering.interactQ[i+1:]...)
			return
		}
	}
}

// resolutionLabel renders one prompt-resolution marker. The runtime
// supplies the reason when it can observe it; interrupted prompts
// fall back to the cause the UI sent itself.
func (m *Model) resolutionLabel(status sessions.PromptStatus, reason string) string {
	if status == sessions.PromptInterrupted && reason == "" {
		reason = interruptReason(m.interruptCause)
	}
	if reason == "" {
		return string(status)
	}
	return string(status) + " (" + reason + ")"
}

// interruptReason maps a cooperative interrupt cause to the label
// shown in the transcript.
func interruptReason(cause agent.Cause) string {
	switch cause {
	case agent.CauseUserInput:
		return "user input"
	case agent.CauseUserCancel:
		return "user cancel"
	case agent.CauseHostShutdown:
		return "host shutdown"
	default:
		return string(cause)
	}
}

func (m *Model) finishInteraction(reply runtime.Reply) {
	if m.answering.interaction == nil {
		return
	}
	ev := m.answering.interaction
	reply.ID = ev.Spec.ID
	m.queue(renderReplyLine(ev.Spec, reply))
	select {
	case ev.ReplyCh <- reply:
	default:
	}
	m.answering.interaction = nil
	m.promoteInteraction()
}

func (m *Model) cancelInteraction() {
	if m.answering.interaction == nil {
		return
	}
	ev := m.answering.interaction
	m.queue(dimStyle.Render("✗ cancelled"))
	select {
	case ev.ReplyCh <- runtime.Reply{
		ID:     ev.Spec.ID,
		Status: runtime.ReplyCancelled,
	}:
	default:
	}
	m.answering.interaction = nil
	m.promoteInteraction()
}

// renderReplyLine renders the printed answer for a finished
// interaction (mirrors the old transcript block).
func renderReplyLine(spec runtime.Spec, reply runtime.Reply) string {
	switch reply.Status {
	case runtime.ReplyCancelled:
		return dimStyle.Render("✗ cancelled")
	default:
		var labels []string
		for _, v := range reply.Options {
			labels = append(labels, optionLabel(spec, v))
		}
		if reply.Option != nil {
			labels = append(labels, optionLabel(spec, *reply.Option))
		}
		switch {
		case len(labels) > 0 && reply.Text != "":
			return toolOKStyle.Render("✓ "+strings.Join(labels, ", ")) + " " +
				userStyle.Render("↩ "+truncate(reply.Text, 200))
		case len(labels) > 0:
			return toolOKStyle.Render("✓ " + strings.Join(labels, ", "))
		case reply.Text != "":
			return userStyle.Render("↩ " + truncate(reply.Text, 400))
		default:
			return dimStyle.Render("✓ answered")
		}
	}
}

func optionLabel(spec runtime.Spec, value string) string {
	for _, o := range spec.Options {
		if o.Value == value {
			return o.Label
		}
	}
	return value
}

// choiceOptions returns the option labels plus one virtual "other"
// entry when the spec allows it.
func (m *Model) choiceOptions() []string {
	ev := m.answering.interaction
	if ev == nil {
		return nil
	}
	opts := make([]string, 0, len(ev.Spec.Options)+1)
	for _, o := range ev.Spec.Options {
		opts = append(opts, o.Label)
	}
	if ev.Spec.AllowOther {
		opts = append(opts, "✎ Other…")
	}
	return opts
}

// ---------- keys ----------

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.selection.active && !m.dragging {
		switch msg.String() {
		case "y":
			text := m.selectedText()
			if text != "" {
				if err := m.copyFn(text); err != nil {
					m.note = "copy failed"
				} else {
					m.note = "copied"
				}
			}
			m.clearSelection()
			return m, nil
		case "esc":
			m.clearSelection()
			return m, nil
		}
	}
	if m.scrollTranscriptKey(msg) {
		return m, nil
	}
	switch m.mode {
	case modeTranscript:
		return m.handleTranscriptKey(msg)
	case modeAnswering:
		return m.handleAnsweringKey(msg)
	case modeResume:
		return m.handleResumeKey(msg)
	case modePermissions:
		return m.handlePermissionsKey(msg)
	case modeSkills:
		return m.handleSkillsKey(msg)
	case modeRunning:
		return m.handleRunningKey(msg)
	default:
		return m.handleIdleKey(msg)
	}
}

// scrollTranscriptKey scrolls the transcript viewport from the
// keyboard: PageUp/PageDown always scroll; Ctrl+U/Ctrl+D and the arrow
// keys scroll when the prompt is empty (so typing and the palette keep
// them). The transcript overlay mode owns its own scrolling and is
// excluded here.
func (m *Model) scrollTranscriptKey(msg tea.KeyMsg) bool {
	if m.mode == modeTranscript {
		return false
	}
	switch msg.String() {
	case "pgup":
		m.viewport.PageUp()
		return true
	case "pgdown":
		m.viewport.PageDown()
		return true
	case "ctrl+u":
		if m.input.Value() == "" {
			m.viewport.HalfPageUp()
			return true
		}
	case "ctrl+d":
		if m.input.Value() == "" {
			m.viewport.HalfPageDown()
			return true
		}
	case "up", "down":
		if m.input.Value() == "" && !m.paletteOpen() &&
			(m.mode == modeIdle || m.mode == modeRunning) {
			if msg.String() == "up" {
				m.viewport.ScrollUp(3)
			} else {
				m.viewport.ScrollDown(3)
			}
			return true
		}
	}
	return false
}

func (m *Model) handleIdleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "ctrl+t":
		m.enterTranscript()
		return m, nil
	case "ctrl+e":
		return m, m.toggleMouseCapture()
	// Newline is Shift+Enter / Option+Enter (codex-rs binds both). The
	// input layer maps them to KeyCtrlJ, so they arrive here as
	// "ctrl+j"; plain Enter stays "enter" and submits. Ctrl+Enter is
	// dropped by the input layer.
	case "ctrl+j":
		m.input.InsertString("\n")
		m.resizeInput()
		m.refreshPalette()
		m.refreshMention()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		if m.mentionOpen() {
			if sk, ok := m.mentionSelection(); ok {
				m.completeMention(sk)
				m.mention.reset()
				return m, nil
			}
		}
		if strings.HasPrefix(text, "/") {
			if name := m.paletteSelection(); name != "" {
				m.palette.reset()
				return m.runCommand(name)
			}
		}
		m.input.Reset()
		m.input.Placeholder = mainPlaceholder
		m.queueUser(text)
		m.drainPending()
		m.setMode(modeRunning)
		return m, m.startTurnCmd(text)
	// Arrow keys navigate the palette. "k"/"j" are deliberately not
	// bound here: the palette input is typed inline, so consuming
	// them would make it impossible to enter any command whose name
	// contains j or k (e.g. /jump). They fall through to the input
	// layer and insert normally.
	case "up", "down":
		if m.paletteOpen() && len(m.palette.results) > 0 {
			n := len(m.palette.results)
			if msg.String() == "up" {
				m.palette.cursor = (m.palette.cursor + n - 1) % n
			} else {
				m.palette.cursor = (m.palette.cursor + 1) % n
			}
			return m, nil
		}
		if m.mentionOpen() && len(m.mention.results) > 0 {
			n := len(m.mention.results)
			if msg.String() == "up" {
				m.mention.cursor = (m.mention.cursor + n - 1) % n
			} else {
				m.mention.cursor = (m.mention.cursor + 1) % n
			}
			return m, nil
		}
	case "esc":
		if m.paletteOpen() {
			m.palette.reset()
			m.input.Reset()
			m.input.Placeholder = mainPlaceholder
			return m, nil
		}
		if m.mentionOpen() {
			m.mention.reset()
			return m, nil
		}
	case "tab":
		if m.paletteOpen() && len(m.palette.results) > 0 {
			m.input.SetValue("/" + m.palette.results[m.palette.cursor])
			m.input.CursorEnd()
			m.refreshPalette()
			return m, nil
		}
		if m.mentionOpen() {
			if sk, ok := m.mentionSelection(); ok {
				m.completeMention(sk)
				m.mention.reset()
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeInput()
	m.refreshPalette()
	m.refreshMention()
	return m, cmd
}

func (m *Model) handleRunningKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.session.turn != nil {
			// Cancel aborts the execution context immediately, so the
			// LLM stream is interrupted mid-output instead of waiting
			// for the next safe checkpoint.
			m.session.turn.Cancel()
			m.note = "cancelling…"
			return m, nil
		}
		return m, tea.Quit
	case "ctrl+t":
		m.enterTranscript()
		return m, nil
	case "ctrl+j":
		m.input.InsertString("\n")
		m.resizeInput()
		return m, nil
	case "esc":
		if m.session.turn != nil {
			intr := agent.Interrupt{
				Cause: agent.CauseUserInput,
			}
			m.interruptCause = intr.Cause
			_ = m.session.turn.Interrupt(intr)
			return m, nil
		}
	case "enter":
		// A turn is in flight; prompt submits are ignored.
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeInput()
	return m, cmd
}

// handleTranscriptKey scrolls the full-output overlay. Esc/Ctrl+T
// return to the mode that was active before it opened.
func (m *Model) handleTranscriptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := m.transcriptLineCount()
	switch msg.String() {
	case "up", "k":
		m.transcript.scroll = max(0, m.transcript.scroll-1)
	case "down", "j":
		m.transcript.scroll = min(max(0, total-1), m.transcript.scroll+1)
	case "pgup", "ctrl+b", "ctrl+u":
		m.transcript.scroll = max(0, m.transcript.scroll-max(1, m.height))
	case "pgdown", "ctrl+f", "ctrl+d":
		m.transcript.scroll = min(
			max(0, total-1), m.transcript.scroll+max(1, m.height))
	case "esc", "ctrl+t":
		m.leaveTranscript()
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// transcriptLineCount returns the total line count of every folded
// block, including separator blanks between blocks.
func (m *Model) transcriptLineCount() int {
	total := 0
	for _, b := range m.transcript.blocks {
		total += len(b) + 1
	}
	return total
}

// enterResumeMode lists the project's stored conversations and shows
// the picker. It returns nil when there is nothing to resume.
func (m *Model) enterResumeMode() tea.Model {
	list, err := m.opts.Sessions.List()
	if err != nil || len(list) == 0 {
		m.queue(dimStyle.Render("No sessions to resume"))
		m.input.Reset()
		m.input.Placeholder = mainPlaceholder
		return m
	}
	m.resume.list = list
	m.resume.cursor = 0
	m.setMode(modeResume)
	return m
}

func (m *Model) handleResumeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.resume.cursor = (m.resume.cursor + len(m.resume.list) - 1) %
			len(m.resume.list)
		return m, nil
	case "down", "j":
		m.resume.cursor = (m.resume.cursor + 1) % len(m.resume.list)
		return m, nil
	case "enter":
		meta := m.resume.list[m.resume.cursor]
		m.opts.ContextID = meta.ID
		if m.opts.Sessions != nil {
			if usage, err := m.opts.Sessions.LoadUsage(m.ctx, meta.ID); err == nil {
				m.usageBase = usage
				m.applyUsageBase()
			}
			// The resumed session carries its own think level; the
			// footer and the next /think cycle pick it up.
			if level, err := m.opts.Sessions.Think(meta.ID); err == nil {
				m.thinkLevel = string(level)
			}
		}
		m.queue(userStyle.Render("↩ Resumed session: " + meta.Title))
		m.flattenHistory(meta.ID)
		m.setMode(modeIdle)
		return m, m.flushPending()
	case "esc", "ctrl+c":
		m.setMode(modeIdle)
		return m, nil
	}
	return m, nil
}

// enterSkillsMode lists the discovered skills and shows the picker.
// Selecting a skill inserts "$name" into the composer so the user can
// extend the prompt; the injection itself happens in the backend.
func (m *Model) enterSkillsMode() tea.Model {
	if m.opts.Skills == nil || !m.opts.Skills.Enabled() {
		m.queue(dimStyle.Render("Skills are disabled"))
		m.input.Reset()
		m.input.Placeholder = mainPlaceholder
		return m
	}
	m.skills.list = m.opts.Skills.List()
	if len(m.skills.list) == 0 && len(m.opts.Skills.Errors()) == 0 {
		m.queue(dimStyle.Render("No skills discovered"))
		m.input.Reset()
		m.input.Placeholder = mainPlaceholder
		return m
	}
	m.skills.cursor = 0
	m.setMode(modeSkills)
	return m
}

// handleSkillsKey drives the skill picker.
func (m *Model) handleSkillsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.skills.cursor = (m.skills.cursor + len(m.skills.list) - 1) %
			len(m.skills.list)
	case "down", "j":
		m.skills.cursor = (m.skills.cursor + 1) % len(m.skills.list)
	case "enter":
		sk := m.skills.list[m.skills.cursor]
		m.setMode(modeIdle)
		m.input.SetValue("$" + sk.Name + " ")
		m.input.CursorEnd()
		return m, nil
	case "esc", "ctrl+c":
		m.setMode(modeIdle)
		return m, nil
	}
	return m, nil
}

// enterPermissionsMode opens the sandbox mode picker, starting at the
// currently active mode.
func (m *Model) enterPermissionsMode() tea.Model {
	m.permissions.cursor = 0
	m.permissions.confirm = false
	if m.currentSandboxMode() == ocsessions.ModeYOLO {
		m.permissions.cursor = 1
	}
	m.setMode(modePermissions)
	return m
}

// handleThinkCommand applies /think: an explicit low|medium|high
// argument sets the level, no argument cycles to the next level. The
// choice is persisted to the session store and printed into the
// stream; the composer returns to idle.
func (m *Model) handleThinkCommand() (tea.Model, tea.Cmd) {
	fields := strings.Fields(m.input.Value())
	arg := ""
	if len(fields) > 1 {
		arg = strings.ToLower(strings.TrimSpace(fields[1]))
	}
	switch arg {
	case "":
		// Cycle low -> medium -> high -> low.
		switch m.thinkLevel {
		case string(ocsessions.ThinkLow):
			arg = string(ocsessions.ThinkMedium)
		case string(ocsessions.ThinkMedium):
			arg = string(ocsessions.ThinkHigh)
		default:
			arg = string(ocsessions.ThinkLow)
		}
	case string(ocsessions.ThinkLow),
		string(ocsessions.ThinkMedium),
		string(ocsessions.ThinkHigh):
	default:
		m.input.Reset()
		m.input.Placeholder = mainPlaceholder
		m.palette.reset()
		m.queue(toolErrStyle.Render(
			"/think: 用法 /think low | medium | high（不带参数循环切换）"))
		return m, m.flushPending()
	}
	m.thinkLevel = arg
	if m.opts.Sessions == nil {
		m.queue(toolOKStyle.Render("think: " + arg))
	} else if err := m.opts.Sessions.SetThink(
		m.opts.ContextID,
		ocsessions.ThinkLevel(arg),
	); err != nil {
		m.queue(toolErrStyle.Render("think: 保存失败: " + err.Error()))
	} else {
		m.queue(toolOKStyle.Render("think: " + arg))
	}
	m.input.Reset()
	m.input.Placeholder = mainPlaceholder
	m.palette.reset()
	m.refreshPalette()
	return m, m.flushPending()
}

// handlePermissionsKey drives the sandbox mode picker. Entering YOLO
// requires an explicit y/Enter confirmation as a second step;
// switching back to workspace applies immediately.
func (m *Model) handlePermissionsKey(
	msg tea.KeyMsg,
) (tea.Model, tea.Cmd) {
	if m.permissions.confirm {
		switch msg.String() {
		case "y", "Y", "enter":
			return m.applyPermissionsMode(ocsessions.ModeYOLO)
		case "n", "N", "esc":
			m.permissions.confirm = false
			m.input.Reset()
			m.input.Placeholder = permissionsPlaceholder
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		// Decrement with wrap; with only two modes the result equals
		// the increment below, but the direction must stay explicit.
		m.permissions.cursor = (m.permissions.cursor - 1 + 2) % 2
	case "down", "j":
		m.permissions.cursor = (m.permissions.cursor + 1) % 2
	case "enter":
		mode := ocsessions.ModeWorkspace
		if m.permissions.cursor == 1 {
			mode = ocsessions.ModeYOLO
		}
		if mode == ocsessions.ModeYOLO {
			m.permissions.confirm = true
			m.input.Reset()
			m.input.Placeholder = "y/Enter confirm · n/Esc cancel"
			return m, nil
		}
		return m.applyPermissionsMode(ocsessions.ModeWorkspace)
	case "esc":
		m.setMode(modeIdle)
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// applyPermissionsMode switches the runtime sandbox mode, prints the
// result into the stream, and returns to idle.
func (m *Model) applyPermissionsMode(
	mode ocsessions.Mode,
) (tea.Model, tea.Cmd) {
	if m.opts.Sessions != nil {
		if err := m.opts.Sessions.SetMode(m.opts.ContextID, mode); err != nil {
			m.queue(toolErrStyle.Render("✗ permission mode: " + err.Error()))
		} else {
			m.queue(userStyle.Render("↩ Permission mode: " + string(mode)))
		}
	}
	m.setMode(modeIdle)
	return m, m.flushPending()
}

// currentSandboxMode returns the active sandbox mode, defaulting to
// workspace when no session store is attached.
func (m *Model) currentSandboxMode() ocsessions.Mode {
	if m.opts.Sessions == nil {
		return ocsessions.ModeWorkspace
	}
	mode, err := m.opts.Sessions.Mode(m.opts.ContextID)
	if err != nil {
		return ocsessions.ModeWorkspace
	}
	return mode
}

// flattenHistory prints the stored conversation into the transcript so
// a resumed session is visible above the input box. User messages use
// the same composer echo as a live Enter submission; assistant
// messages render through the same markdown path as streamed output.
func (m *Model) flattenHistory(id string) {
	if m.opts.Sessions == nil {
		return
	}
	hist, err := m.opts.Sessions.History(m.ctx, id, 1<<30)
	if err != nil {
		return
	}
	// The archive keeps the original message parts (text, reasoning,
	// tool calls, tool results), so resume replays them through the
	// exact same rendering path as live streaming — including call/
	// result pairing by call id. Only user messages use the composer
	// echo box.
	for _, h := range hist {
		if h.Role == message.RoleUser {
			m.queueUser(h.Content.Text())
			continue
		}
		for _, p := range h.Content.Parts {
			m.appendDelta(agent.StreamDeltaPayload{
				Type: agent.StreamDeltaPart,
				Part: p,
			})
		}
		m.flushMarkdown()
	}
}

// resizeInput grows the textarea with its content (wrapped-line
// estimate), capped at a few rows.
func (m *Model) resizeInput() {
	lines := 0
	for _, l := range strings.Split(m.input.Value(), "\n") {
		w := lipgloss.Width(l)
		if w == 0 {
			lines++
		} else {
			lines += (w / max(1, m.width-2)) + 1
		}
	}
	m.input.SetHeight(min(5, max(1, lines)))
}

func (m *Model) handleAnsweringKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ev := m.answering.interaction
	switch ev.Spec.Kind {
	case runtime.KindText:
		switch msg.String() {
		case "ctrl+j":
			m.input.InsertString("\n")
			m.resizeInput()
			return m, nil
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.finishInteraction(runtime.Reply{
				Status: runtime.ReplyOK,
				Text:   text,
			})
			return m, m.flushPending()
		case "esc", "ctrl+c":
			m.cancelInteraction()
			return m, m.flushPending()
		}
	case runtime.KindConfirm, runtime.KindSelect:
		return m.handleChoiceKey(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleChoiceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ev := m.answering.interaction
	if m.answering.selOther {
		switch msg.String() {
		case "ctrl+j":
			m.input.InsertString("\n")
			m.resizeInput()
			return m, nil
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.finishInteraction(runtime.Reply{
				Status: runtime.ReplyOK,
				Text:   text,
			})
			return m, m.flushPending()
		case "esc":
			m.answering.selOther = false
			m.input.Reset()
			m.input.Placeholder = interactionPlaceholder(ev.Spec)
			return m, nil
		case "ctrl+c":
			m.cancelInteraction()
			return m, m.flushPending()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	opts := m.choiceOptions()
	if len(opts) == 0 {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		m.answering.selCursor = (m.answering.selCursor + len(opts) - 1) %
			len(opts)
	case "down", "j":
		m.answering.selCursor = (m.answering.selCursor + 1) % len(opts)
	case " ":
		if ev.Spec.Multi && m.answering.selCursor < len(ev.Spec.Options) {
			idx := m.answering.selCursor
			if m.answering.selSelected[idx] {
				delete(m.answering.selSelected, idx)
			} else {
				m.answering.selSelected[idx] = true
			}
		}
	case "enter":
		if m.answering.selCursor >= len(ev.Spec.Options) {
			m.answering.selOther = true
			m.input.Reset()
			m.input.Placeholder = "Type your own answer… (Enter to send, Esc to go back)"
			m.input.Focus()
			return m, nil
		}
		if ev.Spec.Multi {
			if len(m.answering.selSelected) == 0 {
				return m, nil
			}
			values := make([]string, 0, len(m.answering.selSelected))
			for i, opt := range ev.Spec.Options {
				if m.answering.selSelected[i] {
					values = append(values, opt.Value)
				}
			}
			m.finishInteraction(runtime.Reply{
				Status:  runtime.ReplyOK,
				Options: values,
			})
			return m, m.flushPending()
		}
		m.finishInteraction(runtime.Reply{
			Status: runtime.ReplyOK,
			Option: &ev.Spec.Options[m.answering.selCursor].Value,
		})
		return m, m.flushPending()
	case "y", "Y":
		if ev.Spec.Kind == runtime.KindConfirm {
			m.finishInteraction(runtime.Reply{
				Status: runtime.ReplyOK,
				Option: strPtr("yes"),
			})
			return m, m.flushPending()
		}
	case "n", "N":
		if ev.Spec.Kind == runtime.KindConfirm {
			m.finishInteraction(runtime.Reply{
				Status: runtime.ReplyOK,
				Option: strPtr("no"),
			})
			return m, m.flushPending()
		}
	case "esc", "ctrl+c":
		m.cancelInteraction()
		return m, m.flushPending()
	}
	return m, nil
}

// ---------- turn lifecycle ----------

func (m *Model) startTurnCmd(text string) tea.Cmd {
	return func() tea.Msg {
		key := sessions.Key{AgentID: "assistant", ContextID: m.opts.ContextID}
		lease, err := m.rtc.Runtime().Sessions().Open(m.ctx, key)
		if err != nil {
			return turnDoneMsg{err: err}
		}
		turn, err := lease.Session().Start(m.ctx, agent.Request{
			ContextID: key.ContextID,
			Message:   message.NewTextMessage(message.RoleUser, text),
			// Think level rides the board into the graph's
			// ${board.think_level} inference node reference.
			Inputs: map[string]any{"think_level": m.thinkLevel},
		}, sessions.SinkSpec{
			ID:         "tui",
			Sink:       agent.StreamSinkFunc(m.bridge.Sink),
			QueueSize:  256,
			Visibility: sessions.VisibilityRaw,
			Authority:  sessions.AuthorityObserver,
			AckMode:    sessions.AckOnDelivery,
		})
		if err != nil {
			_ = lease.Close()
			return turnDoneMsg{err: err}
		}
		return turnStartedMsg{lease: lease, turn: turn}
	}
}

func (m *Model) waitCmd() tea.Cmd {
	turn := m.session.turn
	return func() tea.Msg {
		res, err := turn.Wait(m.ctx)
		return turnDoneMsg{res: res, err: err}
	}
}

// appendTurnError queues a turn-level error into the same flushed
// output where the cancel/interrupt markers are printed, so every
// turn outcome lands in one place in the terminal scrollback.
func (m *Model) appendTurnError(err error) {
	runID := ""
	if m.session.turn != nil {
		runID = m.session.turn.RunID()
	}
	reqID := errRequestID(err)
	m.queue(toolErrStyle.Render(
		"✗ [" + errIDs(runID, reqID) + "] " + errDetail(err)))
}

// ---------- view ----------

func (m *Model) View() string {
	m.syncViewport()
	var lines []string
	switch m.mode {
	case modeTranscript:
		// The full-output overlay replaces the viewport and the
		// composer; the status line stays as its header.
		if line := m.statusLine(); line != "" {
			lines = append(lines, line)
		}
		lines = append(lines, m.transcriptView())
		if footer := m.footerLine(); footer != "" {
			lines = append(lines, footer)
		}
		lines = append(lines, "")
		return lipgloss.JoinVertical(lipgloss.Left, lines...)
	}
	// The scrollable transcript fills the screen; the status line,
	// reasoning panel, composer (or picker), footer and a trailing
	// blank row stay pinned below it.
	lines = append(lines, m.viewport.View())
	if line := m.statusLine(); line != "" {
		lines = append(lines, line)
	}
	// The live reasoning tail sits directly above the prompt; it
	// appears only while reasoning is streaming.
	if box := m.reasoningBox(); box != "" {
		lines = append(lines, box)
	}
	switch m.mode {
	case modeResume:
		lines = append(lines, m.resumePicker())
	case modePermissions:
		lines = append(lines, m.permissionsPicker())
	case modeSkills:
		lines = append(lines, m.skillsPicker())
	case modeAnswering:
		ev := m.answering.interaction
		if ev != nil && !m.answering.selOther &&
			(ev.Spec.Kind == runtime.KindSelect ||
				ev.Spec.Kind == runtime.KindConfirm) {
			lines = append(lines, m.interactionSelector())
		} else {
			lines = append(lines, m.composerBar())
		}
	default:
		if m.paletteOpen() {
			lines = append(lines, m.paletteView())
		} else if m.mentionOpen() {
			lines = append(lines, m.mentionView())
		}
		lines = append(lines, m.composerBar())
	}
	// The footer context line (model · project path) stays pinned
	// below the composer in every mode.
	if footer := m.footerLine(); footer != "" {
		lines = append(lines, footer)
	}
	// Trailing blank row: bubbletea's standard renderer erases the
	// cursor's current line (the last rendered row) when the program
	// exits, which would otherwise cut off the composer bar's gray
	// bottom padding. Keeping a real row below the prompt makes that
	// erase hit an empty line instead.
	lines = append(lines, "")
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// footerLine renders the active model and the project path below the
// composer: "deepseek/deepseek-v4-flash · ~/Workspace/opencraft".
func (m *Model) footerLine() string {
	var parts []string
	if m.model != "" {
		parts = append(parts, statusTextStyle.Render(m.model))
	}
	parts = append(parts, dimStyle.Render("effort "+m.thinkLevel))
	if path := displayPath(m.opts.WorkDir); path != "" {
		parts = append(parts, dimStyle.Render(path))
	}
	if !m.mouseCapture {
		parts = append(parts, dimStyle.Render("native select · ctrl+e mouse"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}

// displayPath abbreviates a directory under the user's home to "~",
// e.g. /Users/alice/Workspace/proj -> ~/Workspace/proj. Paths outside
// home are returned unchanged.
func displayPath(dir string) string {
	if dir == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	if strings.HasPrefix(dir, home+string(os.PathSeparator)) {
		return "~" + dir[len(home):]
	}
	return dir
}

// transcriptView renders the full-output overlay: every folded block
// in full, scrollable, above the status line.
func (m *Model) transcriptView() string {
	if len(m.transcript.blocks) == 0 {
		return dimStyle.Render("— nothing folded —")
	}
	var all []string
	for _, b := range m.transcript.blocks {
		all = append(all, b...)
		all = append(all, "")
	}
	// One row is reserved for the footer context line below the
	// overlay so the transcript still fits the terminal height.
	viewH := max(1, m.height-3)
	maxScroll := max(0, len(all)-viewH)
	if m.transcript.scroll > maxScroll {
		m.transcript.scroll = maxScroll
	}
	end := min(len(all), m.transcript.scroll+viewH)
	lines := all[m.transcript.scroll:end]
	header := dimStyle.Render(
		"— full output · ↑/↓ scroll · Esc/Ctrl+T close —")
	footer := dimStyle.Render(fmt.Sprintf(
		"— %d/%d —", m.transcript.scroll+1, len(all)))
	return lipgloss.JoinVertical(lipgloss.Left,
		header, strings.Join(lines, "\n"), footer)
}

// composerBar renders the full-width composer bar. The idle state
// draws the placeholder itself: the textarea's placeholder mode wraps
// the prompt text but never pads its first line to the bar width, so
// the gray background would stop right after the placeholder text
// instead of spanning the terminal.
func (m *Model) composerBar() string {
	contentW := max(1, m.width-2)
	if m.input.Value() == "" && m.input.Placeholder != "" {
		ph := m.input.Placeholder
		first, rest := ph, ""
		if _, size := utf8.DecodeRuneInString(ph); size > 0 {
			first, rest = ph[:size], ph[size:]
		}
		cur := m.input.Cursor
		cur.SetChar(first)
		cur.TextStyle = composerPlaceholderStyle
		line := composerPromptStyle.Render("> ") + cur.View() +
			composerPlaceholderStyle.Render(rest)
		if pad := contentW - lipgloss.Width(line); pad > 0 {
			line += inputTextStyle.Render(strings.Repeat(" ", pad))
		}
		return inputBoxStyle.Render(line)
	}
	return inputBoxStyle.Render(m.input.View())
}

// resumePicker renders the project session list for /resume.
func (m *Model) resumePicker() string {
	rows := []string{reasoningLabelStyle.Render("? Select a session to resume")}
	for i, meta := range m.resume.list {
		prefix := "  "
		if i == m.resume.cursor {
			prefix = "❯ "
		}
		line := fmt.Sprintf("%s%s · %s · %d msgs",
			prefix, truncate(meta.Title, 40),
			meta.UpdatedAt.Format("01-02 15:04"), meta.Messages)
		if i == m.resume.cursor {
			rows = append(rows, userStyle.Render(line))
		} else {
			rows = append(rows, dimStyle.Render(line))
		}
	}
	rows = append(rows, dimStyle.Render("  ↑/↓ choose · Enter resume · Esc cancel"))
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// skillsPicker renders the discovered skill list for /skills.
func (m *Model) skillsPicker() string {
	rows := []string{reasoningLabelStyle.Render("? Select a skill to mention")}
	for i, sk := range m.skills.list {
		prefix := "  "
		if i == m.skills.cursor {
			prefix = "❯ "
		}
		badge := ""
		switch sk.Scope {
		case "builtin":
			badge = " " + dimStyle.Render("[builtin]")
		case "user":
			badge = " " + toolErrStyle.Render("[user]")
		}
		line := fmt.Sprintf("%s%s%s — %s", prefix, sk.Name, badge,
			truncate(sk.Description, 52))
		if i == m.skills.cursor {
			rows = append(rows, userStyle.Render(line))
		} else {
			rows = append(rows, dimStyle.Render(line))
		}
	}
	if errs := m.opts.Skills.Errors(); len(errs) > 0 {
		rows = append(rows, toolErrStyle.Render(
			fmt.Sprintf("  ⚠ %d skill(s) failed to load:", len(errs))))
		n := min(3, len(errs))
		for _, e := range errs[:n] {
			rows = append(rows, dimStyle.Render(
				"    "+truncate(e.Path+": "+e.Message, 60)))
		}
	}
	rows = append(rows, dimStyle.Render(
		"  ↑/↓ choose · Enter insert $name · Esc cancel"))
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// permissionsPicker renders the sandbox mode selector.
func (m *Model) permissionsPicker() string {
	if m.permissions.confirm {
		rows := []string{reasoningLabelStyle.Render(
			"? Switch to YOLO mode?")}
		rows = append(rows,
			toolErrStyle.Render(
				"  Commands run unconfined with the full host environment"),
			toolErrStyle.Render(
				"  and no approval prompts; file tools can reach any path."),
			dimStyle.Render("  y/Enter confirm · n/Esc cancel"))
		return lipgloss.JoinVertical(lipgloss.Left, rows...)
	}
	rows := []string{reasoningLabelStyle.Render("? Select permission mode")}
	modes := []ocsessions.Mode{
		ocsessions.ModeWorkspace,
		ocsessions.ModeYOLO,
	}
	for i, mode := range modes {
		prefix := "  "
		if i == m.permissions.cursor {
			prefix = "❯ "
		}
		line := string(mode)
		if mode == ocsessions.ModeYOLO {
			line += " — no sandbox, full host access, no prompts"
		}
		if i == m.permissions.cursor {
			rows = append(rows, userStyle.Render(prefix+line))
		} else {
			rows = append(rows, dimStyle.Render(prefix+line))
		}
	}
	hint := "↑/↓ choose · Enter apply · Esc cancel"
	rows = append(rows, dimStyle.Render("  "+hint))
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// errIDs renders the run/request identifiers for an error line.
func errIDs(runID, reqID string) string {
	ids := truncate(runID, 12)
	if !strings.HasPrefix(ids, "run-") && !strings.HasPrefix(ids, "run:") {
		ids = "run:" + ids
	}
	if reqID != "" {
		ids += " req:" + truncate(reqID, 16)
	}
	return ids
}

// errRequestID extracts the provider request identifier attached to a
// turn error. Drivers attach it to the chain via errdefs.WithRequestID;
// the inference error also carries it as a field when the provider
// reported one, so both sources are checked.
func errRequestID(err error) string {
	if id, ok := errdefs.RequestID(err); ok {
		return id
	}
	var infErr *inference.Error
	if errors.As(err, &infErr) {
		return infErr.RequestID
	}
	return ""
}

// errDetail renders the concrete failure text for a turn error.
// inference.Error.Error() is deliberately redacted ("provider_failure
// during …" without the cause); the UI appends one level of the
// classified provider cause, which carries the actual message. The
// deeper chain is not rendered because it can contain prompts or wire
// payloads.
func errDetail(err error) string {
	var infErr *inference.Error
	if errors.As(err, &infErr) {
		if cause := errors.Unwrap(infErr); cause != nil {
			if msg := cause.Error(); msg != "" && msg != infErr.Error() {
				return err.Error() + " — " + msg
			}
		}
	}
	return err.Error()
}

// interactionSelector renders the checkbox panel for confirm/select
// interactions.
func (m *Model) interactionSelector() string {
	ev := m.answering.interaction
	opts := m.choiceOptions()
	rows := []string{reasoningLabelStyle.Render("? " + ev.Spec.Title)}
	for i, opt := range opts {
		prefix := "  "
		if i == m.answering.selCursor {
			prefix = "❯ "
		}
		mark := " "
		if i < len(ev.Spec.Options) && m.answering.selSelected[i] {
			mark = "✓"
		}
		line := fmt.Sprintf("%s[%s] %s", prefix, mark, opt)
		if i == m.answering.selCursor {
			rows = append(rows, userStyle.Render(line))
		} else {
			rows = append(rows, dimStyle.Render(line))
		}
	}
	hint := "↑/↓ move · Enter confirm"
	if ev.Spec.Kind == runtime.KindConfirm {
		hint = "↑/↓ or y/n choose · Enter confirm"
	} else if ev.Spec.Multi {
		hint = "↑/↓ move · Space multi-pick · Enter confirm"
	}
	rows = append(rows, dimStyle.Render("  "+hint))
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m *Model) statusLine() string {
	var parts []string
	switch m.mode {
	case modeRunning:
		parts = append(parts, m.spinner.View()+" working")
	case modeAnswering:
		parts = append(parts, statusTextStyle.Render("answering"))
	case modeResume:
		parts = append(parts, statusTextStyle.Render("resume"))
	case modePermissions:
		parts = append(parts, statusTextStyle.Render("permissions"))
	case modeSkills:
		parts = append(parts, statusTextStyle.Render("skills"))
	case modeTranscript:
		parts = append(parts, statusTextStyle.Render("transcript"))
	}
	// The yolo badge stays visible outside the picker so the unconfined
	// mode can never be forgotten mid-session.
	if m.currentSandboxMode() == ocsessions.ModeYOLO &&
		m.mode != modePermissions {
		parts = append(parts, toolErrStyle.Render("yolo"))
	}
	if m.note != "" {
		parts = append(parts, statusTextStyle.Render(m.note))
	}
	if m.usageSeen {
		base := m.usageBase
		parts = append(parts, dimStyle.Render(fmt.Sprintf(
			"in %s · out %s",
			humanInt(base.InputTokens+m.usageIn),
			humanInt(base.OutputTokens+m.usageOut))))
		if m.usageCacheR > 0 || m.usageCacheW > 0 ||
			base.CacheReadTokens > 0 || base.CacheWriteTokens > 0 {
			parts = append(parts, dimStyle.Render(fmt.Sprintf(
				"cache %s/%s",
				humanInt(base.CacheReadTokens+m.usageCacheR),
				humanInt(base.CacheWriteTokens+m.usageCacheW))))
		}
		parts = append(parts, dimStyle.Render(fmt.Sprintf(
			"think %s · %s",
			humanInt(base.ReasoningTokens+m.usageThink),
			humanLatency(base.LatencyMs+m.usageLat))))
		if base.InputTokens+m.usageIn > 0 {
			chr := 100 * float64(base.CacheReadTokens+m.usageCacheR) /
				float64(base.InputTokens+m.usageIn)
			parts = append(parts, dimStyle.Render(fmt.Sprintf("CHR %.2f%%", chr)))
		}
	}
	// Scrolled back into history: a quiet marker so the reader knows
	// the viewport is no longer following the live stream.
	if len(m.log) > 0 && !m.viewport.AtBottom() {
		parts = append(parts, statusTextStyle.Render("↑ history"))
	}
	if m.selection.active && !m.dragging {
		parts = append(parts, statusTextStyle.Render("y copy · Esc clear"))
	}
	return dimStyle.Render(strings.Join(parts, " · "))
}

// sessionUsage returns the cumulative usage of the active session:
// the resumed baseline plus the live turn counters.
func (m *Model) sessionUsage() ocsessions.Usage {
	return ocsessions.Usage{
		InputTokens:      m.usageBase.InputTokens + m.usageIn,
		OutputTokens:     m.usageBase.OutputTokens + m.usageOut,
		TotalTokens:      m.usageBase.TotalTokens + m.usageTotal,
		CacheReadTokens:  m.usageBase.CacheReadTokens + m.usageCacheR,
		CacheWriteTokens: m.usageBase.CacheWriteTokens + m.usageCacheW,
		ReasoningTokens:  m.usageBase.ReasoningTokens + m.usageThink,
		LatencyMs:        m.usageBase.LatencyMs + m.usageLat,
	}
}

// applyUsageBase shows the resumed session's cumulative usage in the
// status line while it is idle (before the next turn starts).
func (m *Model) applyUsageBase() {
	m.usageSeen = m.usageBase != (ocsessions.Usage{})
}

func humanInt(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}

func humanLatency(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d >= time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

func strPtr(s string) *string { return &s }

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// truncateWidth truncates s to at most n display cells, appending an
// ellipsis when anything was cut. Unlike truncate (rune count) this is
// display-width aware, so CJK text cannot overrun the reasoning panel.
func truncateWidth(s string, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > n {
			return b.String() + "…"
		}
		b.WriteRune(r)
		w += rw
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
