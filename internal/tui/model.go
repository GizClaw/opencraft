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

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/message"
	sessions "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/interact"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/tools/applypatch"
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

// flushPrintMsg drains the pending output queue into the terminal
// above the prompt (stdout-style streaming).
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
const mainPlaceholder = "Ask opencraft…"

// resumePlaceholder is the picker hint shown in /resume mode.
const resumePlaceholder = "↑/↓ pick session · Enter resume · Esc cancel"

// mode is the explicit top-level UI state. Keyboard routing and the
// status line derive from it instead of scattered boolean flags.
type mode int

const (
	modeIdle mode = iota
	modeRunning
	modeAnswering
	modeResume
	modeTranscript
)

// sessionState is the active turn context; it is nil when idle.
type sessionState struct {
	lease *sessions.Lease
	turn  *sessions.Turn
}

// streamState carries the transcript buffers for the current turn: the
// print queue plus the in-flight text/reasoning/tool lines. It is
// drained to the terminal by flushPending and reset at turn boundaries.
type streamState struct {
	pending      []string
	mdBuf        string
	reasoningBuf string
	// msgOpen is true while an assistant text message is streaming;
	// it is closed by the next boundary (tool call, question, turn
	// end) so the message rule is printed below the last paragraph.
	msgOpen      bool
	lastTool     string
	pendingCalls map[string]pendingCall
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

// transcriptState is live while mode == modeTranscript: the full
// content of every block that was folded on screen, scrollable above
// the prompt.
type transcriptState struct {
	blocks [][]string
	scroll int
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

// Model is the stdout-style REPL: agent output streams into the
// terminal scrollback above a single prompt line. The View only ever
// covers the status line plus the input line (or the interaction
// selector / resume picker), never the full screen.
type Model struct {
	rtc     *app.RuntimeController
	opts    Options
	ctx     context.Context
	program *tea.Program
	bridge  *Bridge
	broker  *interact.Broker

	mode mode

	// session is the active turn (nil when idle).
	session sessionState

	// stream holds the turn-bounded transcript buffers.
	stream streamState

	// answering is live while mode == modeAnswering.
	answering answeringState

	// resume is live while mode == modeResume.
	resume resumeState

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

	// display: model id, token usage and transient status annotation.
	model       string
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

	input   textarea.Model
	spinner spinner.Model

	width  int
	height int
}

// New creates the stdout-REPL model over the bridge and interaction
// broker.
func New(
	rtc *app.RuntimeController,
	opts Options,
	bridge *Bridge,
	broker *interact.Broker,
) *Model {
	input := newInput(mainPlaceholder)
	input.Focus()

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = spinnerStyle

	return &Model{
		rtc:       rtc,
		opts:      opts,
		ctx:       context.Background(),
		bridge:    bridge,
		broker:    broker,
		model:     opts.Model,
		input:     input,
		spinner:   spin,
		answering: answeringState{selSelected: make(map[int]bool)},
		stream:    streamState{pendingCalls: make(map[string]pendingCall)},
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
	case modeResume:
		m.input.Reset()
		m.input.Placeholder = resumePlaceholder
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
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

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
		return m, tea.Batch(m.flushPending(), m.flushTick())

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
				m.stream.pending = append(m.stream.pending, c.lines...)
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
				m.stream.pending = append(m.stream.pending, toolErrStyle.Render(
					"✗ cancelled ["+runID+"]"))
			case agent.StatusInterrupted:
				m.stream.pending = append(m.stream.pending, toolErrStyle.Render(
					"✗ interrupted ["+runID+"]"))
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
		m.chatResolved(ev.Resolved.ID, ev.Resolved.Status)
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
			m.stream.pending = append(m.stream.pending, lines...)
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
		m.stream.pending = append(
			m.stream.pending, m.foldBlock(header, body, end)...)
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
		m.stream.pending = append(m.stream.pending, m.messageRule()...)
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
		m.stream.pending = append(m.stream.pending, m.messageRule()...)
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
	m.stream.pending = append(m.stream.pending, m.foldBlock(nil, lines, "")...)
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

// messageRule returns the separator framing an assistant message
// block: a blank line, a full-width white rule and another blank
// line, so the rule keeps one line of breathing room from the content
// on either side.
func (m *Model) messageRule() []string {
	return []string{
		"",
		assistantRuleStyle.Render(strings.Repeat("─", max(1, m.width))),
		"",
	}
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

// flushPending prints every queued line above the prompt.
func (m *Model) flushPending() tea.Cmd {
	if len(m.stream.pending) == 0 {
		return nil
	}
	lines := m.stream.pending
	m.stream.pending = nil
	return tea.Println(eraseEOL(strings.Join(lines, "\n")))
}

// eraseEOL appends an erase-to-end-of-line after every line. A queued
// output line longer than the terminal wraps, and the wrapped rows
// keep whatever was underneath; without the erase the tail of a
// wrapped line would retain stale background cells from the full-width
// composer bar.
func eraseEOL(s string) string {
	return strings.ReplaceAll(s, "\n", "\x1b[K\n") + "\x1b[K"
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
	m.stream.pending = append(m.stream.pending,
		reasoningLabelStyle.Render("? "+ev.Spec.Title))
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
	if ev.Spec.Kind == interact.KindConfirm && len(ev.Spec.Options) == 0 {
		ev.Spec.Options = []interact.Option{
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

func interactionPlaceholder(spec interact.Spec) string {
	switch spec.Kind {
	case interact.KindText:
		return "Type your answer… (Enter to send, Esc to cancel)"
	case interact.KindConfirm:
		return "↑/↓ or y/n choose · Enter confirm · Esc cancel"
	default:
		return "↑/↓ choose · Space multi-pick · Enter confirm · Esc cancel"
	}
}

func (m *Model) chatResolved(id string, status sessions.PromptStatus) {
	if m.answering.interaction != nil && m.answering.interaction.Spec.ID == id {
		m.stream.pending = append(m.stream.pending,
			dimStyle.Render("✗ "+string(status)))
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

func (m *Model) finishInteraction(reply interact.Reply) {
	if m.answering.interaction == nil {
		return
	}
	ev := m.answering.interaction
	reply.ID = ev.Spec.ID
	m.stream.pending = append(m.stream.pending, renderReplyLine(ev.Spec, reply))
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
	m.stream.pending = append(m.stream.pending, dimStyle.Render("✗ cancelled"))
	select {
	case ev.ReplyCh <- interact.Reply{
		ID:     ev.Spec.ID,
		Status: interact.ReplyCancelled,
	}:
	default:
	}
	m.answering.interaction = nil
	m.promoteInteraction()
}

// renderReplyLine renders the printed answer for a finished
// interaction (mirrors the old transcript block).
func renderReplyLine(spec interact.Spec, reply interact.Reply) string {
	switch reply.Status {
	case interact.ReplyCancelled:
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

func optionLabel(spec interact.Spec, value string) string {
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
	switch m.mode {
	case modeTranscript:
		return m.handleTranscriptKey(msg)
	case modeAnswering:
		return m.handleAnsweringKey(msg)
	case modeResume:
		return m.handleResumeKey(msg)
	case modeRunning:
		return m.handleRunningKey(msg)
	default:
		return m.handleIdleKey(msg)
	}
}

func (m *Model) handleIdleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "ctrl+t":
		m.enterTranscript()
		return m, nil
	// Newline is Shift+Enter / Option+Enter (codex-rs binds both). The
	// input layer maps them to KeyCtrlJ, so they arrive here as
	// "ctrl+j"; plain Enter stays "enter" and submits. Ctrl+Enter is
	// dropped by the input layer.
	case "ctrl+j":
		m.input.InsertString("\n")
		m.resizeInput()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		if text == "/resume" {
			return m.enterResumeMode(), nil
		}
		m.input.Reset()
		m.input.Placeholder = mainPlaceholder
		echo := tea.Println(m.renderUserEcho(text))
		m.setMode(modeRunning)
		return m, tea.Batch(echo, m.startTurnCmd(text))
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeInput()
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
			_ = m.session.turn.Interrupt(agent.Interrupt{
				Cause: agent.CauseUserInput,
			})
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
		m.stream.pending = append(m.stream.pending,
			dimStyle.Render("No sessions to resume"))
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
		}
		m.stream.pending = append(m.stream.pending,
			userStyle.Render("↩ Resumed session: "+meta.Title))
		m.flattenHistory(meta.ID)
		m.setMode(modeIdle)
		return m, m.flushPending()
	case "esc", "ctrl+c":
		m.setMode(modeIdle)
		return m, nil
	}
	return m, nil
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
	for _, h := range hist {
		var reasoning, text string
		for _, p := range h.Content.Parts {
			switch part := p.(type) {
			case message.ReasoningPart:
				reasoning += part.Text
			case message.TextPart:
				text += part.Text
			}
		}
		if reasoning != "" {
			m.transcript.append(m.reasoningHistory(
				strings.TrimSpace(reasoning)))
		}
		if text == "" {
			continue
		}
		if h.Role == message.RoleUser {
			m.stream.pending = append(m.stream.pending,
				m.renderUserEcho(text))
		} else {
			m.appendMarkdown(text)
			m.flushMarkdown()
		}
	}
}

// renderUserEcho renders the submitted message exactly like the
// composer bar: full-width gray background, one-cell padding, first
// line prefixed with "> ".
func (m *Model) renderUserEcho(text string) string {
	lines := strings.Split(text, "\n")
	contentW := max(1, m.width-2)
	for i, l := range lines {
		if i == 0 {
			lines[i] = composerPromptStyle.Render("> ") + inputTextStyle.Render(l)
		} else {
			lines[i] = inputTextStyle.Render("  " + l)
		}
		if pad := contentW - lipgloss.Width(lines[i]); pad > 0 {
			lines[i] += inputTextStyle.Render(strings.Repeat(" ", pad))
		}
	}
	return inputBoxStyle.Render(strings.Join(lines, "\n"))
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
	case interact.KindText:
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
			m.finishInteraction(interact.Reply{
				Status: interact.ReplyOK,
				Text:   text,
			})
			return m, m.flushPending()
		case "esc", "ctrl+c":
			m.cancelInteraction()
			return m, m.flushPending()
		}
	case interact.KindConfirm, interact.KindSelect:
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
			m.finishInteraction(interact.Reply{
				Status: interact.ReplyOK,
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
			m.finishInteraction(interact.Reply{
				Status:  interact.ReplyOK,
				Options: values,
			})
			return m, m.flushPending()
		}
		m.finishInteraction(interact.Reply{
			Status: interact.ReplyOK,
			Option: &ev.Spec.Options[m.answering.selCursor].Value,
		})
		return m, m.flushPending()
	case "y", "Y":
		if ev.Spec.Kind == interact.KindConfirm {
			m.finishInteraction(interact.Reply{
				Status: interact.ReplyOK,
				Option: strPtr("yes"),
			})
			return m, m.flushPending()
		}
	case "n", "N":
		if ev.Spec.Kind == interact.KindConfirm {
			m.finishInteraction(interact.Reply{
				Status: interact.ReplyOK,
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
	m.stream.pending = append(m.stream.pending, toolErrStyle.Render(
		"✗ ["+errIDs(runID, reqID)+"] "+errDetail(err)))
}

// ---------- view ----------

func (m *Model) View() string {
	lines := []string{m.statusLine()}
	// The live reasoning tail sits directly above the prompt; it
	// appears only while reasoning is streaming.
	if box := m.reasoningBox(); box != "" {
		lines = append(lines, box)
	}
	switch m.mode {
	case modeResume:
		lines = append(lines, m.resumePicker())
	case modeTranscript:
		lines = append(lines, m.transcriptView())
	case modeAnswering:
		ev := m.answering.interaction
		if ev != nil && !m.answering.selOther &&
			(ev.Spec.Kind == interact.KindSelect ||
				ev.Spec.Kind == interact.KindConfirm) {
			lines = append(lines, m.interactionSelector())
		} else {
			lines = append(lines, m.composerBar())
		}
	default:
		lines = append(lines, m.composerBar())
	}
	// Trailing blank row: bubbletea's standard renderer erases the
	// cursor's current line (the last rendered row) when the program
	// exits, which would otherwise cut off the composer bar's gray
	// bottom padding. Keeping a real row below the prompt makes that
	// erase hit an empty line instead.
	lines = append(lines, "")
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
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
	viewH := max(1, m.height-2)
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
	if ev.Spec.Kind == interact.KindConfirm {
		hint = "↑/↓ or y/n choose · Enter confirm"
	} else if ev.Spec.Multi {
		hint = "↑/↓ move · Space multi-pick · Enter confirm"
	}
	rows = append(rows, dimStyle.Render("  "+hint))
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m *Model) statusLine() string {
	var parts []string
	if m.model != "" {
		parts = append(parts, statusTextStyle.Render(m.model))
	}
	switch m.mode {
	case modeRunning:
		parts = append(parts, m.spinner.View()+" working")
	case modeAnswering:
		parts = append(parts, statusTextStyle.Render("answering"))
	case modeResume:
		parts = append(parts, statusTextStyle.Render("resume"))
	case modeTranscript:
		parts = append(parts, statusTextStyle.Render("transcript"))
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
	if len(parts) == 0 {
		return identityStyle.Render("opencraft")
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
