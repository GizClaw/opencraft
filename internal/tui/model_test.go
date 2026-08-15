package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	sessions "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/interact"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
)

func newTestModel() *Model {
	return New(nil, Options{Model: "deepseek/x"}, NewBridge(16), nil)
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello world", 5); got != "hello…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("你好", 10); got != "你好" {
		t.Errorf("truncate unicode = %q", got)
	}
}

func TestEmptyEnterNoSubmit(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("empty enter should not submit")
	}
}

func TestCtrlCQuitsWhenIdle(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("cmd = %T, want QuitMsg", cmd())
	}
}

func TestViewRendersStatusAndPrompt(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 24
	v := m.View()
	if !strings.Contains(v, "deepseek/x") || !strings.Contains(v, ">") {
		t.Errorf("view = %q", v)
	}
}

func TestStatusLineShowsCacheHitRate(t *testing.T) {
	m := newTestModel()
	m.applyEvent(Event{Usage: &UsageEvent{
		InputTokens:      800,
		CacheReadTokens:  500,
		CacheWriteTokens: 100,
		ReasoningTokens:  120,
		LatencyMs:        300,
	}})
	line := m.statusLine()
	if !strings.Contains(line, "CHR 62.50%") {
		t.Errorf("status line = %q, want CHR 62.50%%", line)
	}
	if !strings.Contains(line, "think 120") {
		t.Errorf("status line = %q, want think 120", line)
	}
}

func TestStatusLineOmitsChrWithoutInput(t *testing.T) {
	m := newTestModel()
	m.applyEvent(Event{Usage: &UsageEvent{
		OutputTokens: 10,
		TotalTokens:  10,
	}})
	if strings.Contains(m.statusLine(), "CHR") {
		t.Errorf("status line = %q, want no CHR", m.statusLine())
	}
}

func TestRenderUserEcho(t *testing.T) {
	m := newTestModel()
	m.width = 80
	got := m.renderUserEcho("hello\nworld")
	if !strings.Contains(got, "> hello") || !strings.Contains(got, "world") {
		t.Errorf("echo = %q", got)
	}
	if !strings.Contains(got, "  world") {
		t.Errorf("continuation line should align: %q", got)
	}
}

func TestErrorLineRenders(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.errs.lastErr = "provider boom"
	m.errs.lastErrRun = "run123"
	m.errs.lastErrReq = "req456"
	v := m.View()
	if !strings.Contains(v, "✗ [run:run123 req:req456] provider boom") {
		t.Errorf("view = %q", v)
	}
}

func TestTurnDoneCapturesRequestID(t *testing.T) {
	m := newTestModel()
	err := errdefs.WithRequestID(errors.New("provider boom"), "req-xyz")
	m.Update(turnDoneMsg{err: err})
	if m.errs.lastErrReq != "req-xyz" {
		t.Errorf("lastErrReq = %q", m.errs.lastErrReq)
	}
	if !strings.Contains(m.View(), "req:req-xyz") {
		t.Errorf("view = %q", m.View())
	}
}

func TestTurnDoneCancelMarker(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(turnDoneMsg{res: &agent.Result{Status: agent.StatusCanceled}})
	if cmd == nil || !strings.Contains(fmt.Sprintf("%#v", cmd()), "✗ cancelled") {
		t.Errorf("flush cmd = %#v", cmd)
	}
}

func TestTurnDoneInterruptMarker(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(turnDoneMsg{res: &agent.Result{Status: agent.StatusInterrupted}})
	if cmd == nil || !strings.Contains(fmt.Sprintf("%#v", cmd()), "✗ interrupted") {
		t.Errorf("flush cmd = %#v", cmd)
	}
}

func TestResumePicker(t *testing.T) {
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(context.Background(), id, []message.Message{
		message.NewTextMessage(message.RoleUser, "你好"),
	}); err != nil {
		t.Fatal(err)
	}

	m := New(nil, Options{Model: "deepseek/x", Sessions: store},
		NewBridge(16), nil)
	m.input.SetValue("/resume")
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if next.mode != modeResume || len(next.resume.list) != 1 {
		t.Fatalf("resume mode not active: mode=%v list=%+v",
			next.mode, next.resume.list)
	}
	if !strings.Contains(next.View(), "Select a session to resume") {
		t.Error("resume picker not rendered")
	}
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(*Model)
	if next.mode != modeIdle || next.opts.ContextID != id {
		t.Fatalf("resume failed: mode=%v id=%q want %q",
			next.mode, next.opts.ContextID, id)
	}
}

func TestResumeFlattensHistory(t *testing.T) {
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(context.Background(), id, []message.Message{
		message.NewTextMessage(message.RoleUser, "你好"),
		{Role: message.RoleAssistant, Content: message.Content{Parts: []message.Part{
			message.TextPart{Text: "你好！"},
		}}},
	}); err != nil {
		t.Fatal(err)
	}

	m := New(nil, Options{Model: "deepseek/x", Sessions: store},
		NewBridge(16), nil)
	m.input.SetValue("/resume")
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	updated, cmd := next.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(*Model)
	if next.opts.ContextID != id {
		t.Fatalf("context id = %q, want %q", next.opts.ContextID, id)
	}
	if cmd == nil {
		t.Fatal("resume should flush history")
	}
	out := fmt.Sprintf("%#v", cmd())
	if !strings.Contains(out, "> 你好") || !strings.Contains(out, "你好！") {
		t.Errorf("history not flattened: %s", out)
	}
}

func TestEnterSubmitsText(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("hello")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd == nil {
		t.Fatal("enter should return a submit command")
	}
	if next.input.Value() != "" {
		t.Errorf("input not reset: %q", next.input.Value())
	}
	if len(next.stream.pending) != 0 {
		t.Errorf("echo should print directly, pending = %v",
			next.stream.pending)
	}
}

func TestEnterSubmitsTransitionsToRunning(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("hello")
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if next.mode != modeRunning {
		t.Fatalf("mode = %v, want running", next.mode)
	}
	if !strings.Contains(next.View(), "working") {
		t.Errorf("status line = %q", next.View())
	}
}

func TestRunningKeySwallowsEnter(t *testing.T) {
	m := newTestModel()
	m.mode = modeRunning
	m.input.SetValue("hello")
	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if cmd != nil {
		t.Fatalf("enter during running should be ignored, cmd = %v", cmd)
	}
	if next.input.Value() != "hello" || next.mode != modeRunning {
		t.Errorf("input/mode changed: %q / %v", next.input.Value(), next.mode)
	}
}

func TestCtrlCWhileRunningWithoutTurnQuits(t *testing.T) {
	m := newTestModel()
	m.mode = modeRunning
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c with no turn should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("cmd = %T, want QuitMsg", cmd())
	}
}

func TestCtrlTRequiresFoldedContent(t *testing.T) {
	m := newTestModel()
	m.mode = modeRunning
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlT})
	next := updated.(*Model)
	if next.mode != modeRunning {
		t.Fatalf("mode = %v, want running (no folded content)", next.mode)
	}
	m.transcript.blocks = [][]string{{"a", "b", "c"}}
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlT})
	next = updated.(*Model)
	if next.mode != modeTranscript {
		t.Fatalf("mode = %v, want transcript", next.mode)
	}
}

func TestTurnDoneReturnsToIdle(t *testing.T) {
	m := newTestModel()
	m.mode = modeRunning
	m.stream.mdBuf = "unfinished paragraph"
	m.stream.pendingCalls = map[string]pendingCall{
		"c1": {name: "tool_a", lines: []string{"⚙ tool_a"}},
	}
	m.Update(turnDoneMsg{res: &agent.Result{Status: agent.StatusCanceled}})
	if m.mode != modeIdle {
		t.Fatalf("mode = %v, want idle", m.mode)
	}
	// The drained lines survive the turn; the in-flight buffers reset.
	if len(m.stream.pendingCalls) != 0 {
		t.Errorf("pendingCalls not drained: %v", m.stream.pendingCalls)
	}
}

func TestExitRunningKeepsBuffersForActiveTurn(t *testing.T) {
	m := newTestModel()
	m.mode = modeRunning
	m.session.turn = &sessions.Turn{}
	m.stream.mdBuf = "partial"
	m.stream.reasoningBuf = "think"
	m.setMode(modeAnswering)
	if m.stream.mdBuf != "partial" || m.stream.reasoningBuf != "think" {
		t.Errorf("buffers lost on running -> answering: %q / %q",
			m.stream.mdBuf, m.stream.reasoningBuf)
	}
	// A finished turn (handle nil) clears the text buffers on exit.
	m.session.turn = nil
	m.setMode(modeRunning)
	m.stream.mdBuf = "partial"
	m.setMode(modeIdle)
	if m.stream.mdBuf != "" {
		t.Errorf("buffers not cleared at turn end: %q", m.stream.mdBuf)
	}
}

func TestInteractionReturnsToRunning(t *testing.T) {
	m := newTestModel()
	m.mode = modeRunning
	replyCh := make(chan interact.Reply, 1)
	m.dispatch(Event{Interact: &InteractEvent{
		Spec:    interact.Spec{ID: "p1", Kind: interact.KindText, Title: "q"},
		ReplyCh: replyCh,
	}})
	if m.mode != modeAnswering {
		t.Fatalf("mode = %v, want answering", m.mode)
	}
	if !strings.Contains(m.View(), "answering") {
		t.Errorf("status line = %q", m.View())
	}
	m.input.SetValue("ok")
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeRunning {
		t.Fatalf("mode = %v, want running", m.mode)
	}
	if m.answering.interaction != nil || m.answering.selSelected == nil {
		t.Errorf("answering state not reset: %+v", m.answering)
	}
}

func TestResumeEscReturnsToIdle(t *testing.T) {
	m := newTestModel()
	m.mode = modeResume
	m.resume.list = []ocsessions.Meta{{ID: "s1", Title: "会话"}}
	m.resume.cursor = 0
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(*Model)
	if next.mode != modeIdle || len(next.resume.list) != 0 {
		t.Fatalf("esc from resume: mode=%v list=%v", next.mode, next.resume.list)
	}
	if next.input.Placeholder != mainPlaceholder {
		t.Errorf("placeholder = %q", next.input.Placeholder)
	}
}

func TestResumeRestoresSessionUsage(t *testing.T) {
	m := newTestModel()
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUsage(context.Background(), id, ocsessions.Usage{
		InputTokens:     800,
		OutputTokens:    200,
		CacheReadTokens: 500,
		ReasoningTokens: 120,
		LatencyMs:       300,
	}); err != nil {
		t.Fatal(err)
	}
	m.opts.Sessions = store
	m.mode = modeResume
	m.resume.list = []ocsessions.Meta{{ID: id, Title: "会话"}}
	m.resume.cursor = 0
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if next.opts.ContextID != id {
		t.Fatalf("context id = %q, want %q", next.opts.ContextID, id)
	}
	line := next.statusLine()
	if !strings.Contains(line, "in 800 · out 200") {
		t.Errorf("status line = %q, want restored in/out", line)
	}
	if !strings.Contains(line, "CHR 62.50%") {
		t.Errorf("status line = %q, want restored CHR", line)
	}
}

func TestSessionUsagePersistsBasePlusLive(t *testing.T) {
	m := newTestModel()
	m.usageBase = ocsessions.Usage{
		InputTokens:     800,
		OutputTokens:    200,
		CacheReadTokens: 500,
		ReasoningTokens: 120,
		LatencyMs:       300,
	}
	m.usageIn = 200
	m.usageOut = 50
	m.usageCacheR = 100
	m.usageThink = 30
	m.usageLat = 100
	got := m.sessionUsage()
	if got.InputTokens != 1000 || got.OutputTokens != 250 ||
		got.CacheReadTokens != 600 || got.ReasoningTokens != 150 ||
		got.LatencyMs != 400 {
		t.Errorf("session usage = %+v", got)
	}
}

func TestUsageBaseAccumulatesAcrossTurns(t *testing.T) {
	m := newTestModel()
	store, err := ocsessions.New(filepath.Join(t.TempDir(), "sessions"), 40)
	if err != nil {
		t.Fatal(err)
	}
	m.opts.Sessions = store

	// Turn 1: one usage report, then the turn ends. The recorded total
	// must become the base for the next turn.
	m.applyEvent(Event{Usage: &UsageEvent{
		InputTokens:  300,
		OutputTokens: 100,
		TotalTokens:  400,
		LatencyMs:    50,
	}})
	_, _ = m.Update(turnDoneMsg{
		res: &agent.Result{Status: agent.StatusCompleted},
	})
	if m.usageBase.InputTokens != 300 {
		t.Fatalf("base after turn 1 = %+v, want in=300", m.usageBase)
	}

	// Turn 2 starts with reset live counters; new usage must add to the
	// base instead of replacing it.
	m.usageIn, m.usageOut, m.usageTotal = 0, 0, 0
	m.usageSeen = false
	m.applyEvent(Event{Usage: &UsageEvent{
		InputTokens:  200,
		OutputTokens: 50,
		TotalTokens:  250,
		LatencyMs:    25,
	}})
	_, _ = m.Update(turnDoneMsg{
		res: &agent.Result{Status: agent.StatusCompleted},
	})
	if m.usageBase.InputTokens != 500 || m.usageBase.OutputTokens != 150 {
		t.Fatalf("base after turn 2 = %+v, want in=500 out=150", m.usageBase)
	}

	loaded, err := store.LoadUsage(context.Background(), m.opts.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.InputTokens != 500 || loaded.OutputTokens != 150 {
		t.Fatalf("persisted usage = %+v, want cumulative in=500 out=150", loaded)
	}
}

func TestStreamMarkdownParagraphs(t *testing.T) {
	m := newTestModel()
	m.appendDelta(textDelta("**hi**\n\nworld"))
	// blank + white rule + blank open the message block, then the
	// completed paragraph follows.
	if len(m.stream.pending) != 4 {
		t.Fatalf("pending = %+v", m.stream.pending)
	}
	if !strings.Contains(m.stream.pending[3], "hi") {
		t.Errorf("rendered paragraph = %q", m.stream.pending[3])
	}
	if m.stream.mdBuf != "world" {
		t.Errorf("mdBuf = %q", m.stream.mdBuf)
	}
	m.flushMarkdown()
	// The remaining paragraph plus the closing blank/rule/blank.
	if len(m.stream.pending) != 8 ||
		m.stream.pending[4] != "world" ||
		m.stream.pending[5] != "" ||
		!strings.Contains(m.stream.pending[6], "─") ||
		m.stream.pending[7] != "" {
		t.Errorf("pending = %+v", m.stream.pending)
	}
}

func TestAssistantMessageRules(t *testing.T) {
	m := newTestModel()
	m.width = 40
	m.appendDelta(textDelta("hello"))
	// Only the opening blank/rule/blank is pending; the text stays
	// buffered until a paragraph completes or the message flushes.
	if len(m.stream.pending) != 3 {
		t.Fatalf("pending = %+v", m.stream.pending)
	}
	if m.stream.pending[0] != "" ||
		!strings.Contains(m.stream.pending[1], "─") ||
		m.stream.pending[2] != "" {
		t.Errorf("message opening = %+v", m.stream.pending)
	}
	if w := lipgloss.Width(m.stream.pending[1]); w != m.width {
		t.Errorf("rule width = %d, want %d", w, m.width)
	}
	// A tool call closes the block; the next answer opens a new one.
	m.appendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolCallPart{Call: message.ToolCall{
			Name: "exec_command", Arguments: []byte(`{"command":"pwd"}`),
		}},
	})
	if len(m.stream.pending) != 8 {
		t.Fatalf("pending after tool call = %+v", m.stream.pending)
	}
	if m.stream.pending[4] != "" ||
		!strings.Contains(m.stream.pending[5], "─") ||
		m.stream.pending[6] != "" ||
		!strings.Contains(m.stream.pending[3], "hello") ||
		!strings.Contains(m.stream.pending[7], "Ran pwd") {
		t.Errorf("message closing = %+v", m.stream.pending)
	}
	if m.stream.msgOpen {
		t.Error("message still open after tool call")
	}
	m.appendDelta(textDelta("again"))
	if len(m.stream.pending) != 11 {
		t.Fatalf("pending after second answer = %+v", m.stream.pending)
	}
	if m.stream.pending[8] != "" ||
		!strings.Contains(m.stream.pending[9], "─") ||
		m.stream.pending[10] != "" {
		t.Errorf("second message opening missing: %+v", m.stream.pending)
	}
	m.flushMarkdown()
	if len(m.stream.pending) != 15 ||
		!strings.Contains(m.stream.pending[11], "again") ||
		m.stream.pending[12] != "" ||
		!strings.Contains(m.stream.pending[13], "─") ||
		m.stream.pending[14] != "" {
		t.Errorf("pending after flush = %+v", m.stream.pending)
	}
	if m.stream.msgOpen {
		t.Error("message not closed by flushMarkdown")
	}
}

func TestStreamOrderingReasoningBeforeText(t *testing.T) {
	m := newTestModel()
	m.appendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ReasoningPart{Text: "think think think"},
	})
	m.appendDelta(textDelta("answer\n\nnext paragraph"))
	// Reasoning must not print into the scrollback; it lives only in
	// the live panel and the archived transcript overlay.
	for _, line := range m.stream.pending {
		if strings.Contains(line, "think") {
			t.Fatalf("reasoning leaked into scrollback: %v", m.stream.pending)
		}
	}
	if len(m.transcript.blocks) != 1 {
		t.Fatalf("reasoning not archived: %d blocks", len(m.transcript.blocks))
	}
	if !strings.Contains(strings.Join(m.transcript.blocks[0], "\n"), "think") {
		t.Errorf("archived reasoning missing: %v", m.transcript.blocks[0])
	}
	if m.stream.reasoningBuf != "" {
		t.Errorf("live panel not cleared: %q", m.stream.reasoningBuf)
	}
}

func TestReasoningArchived(t *testing.T) {
	m := newTestModel()
	m.width = 60
	m.appendReasoning("line one\nline two")
	m.archiveReasoning()
	if m.stream.reasoningBuf != "" {
		t.Errorf("buffer not drained: %q", m.stream.reasoningBuf)
	}
	if len(m.transcript.blocks) != 1 {
		t.Fatalf("reasoning not archived: %v", m.transcript.blocks)
	}
	block := strings.Join(m.transcript.blocks[0], "\n")
	if !strings.Contains(block, "line one") ||
		!strings.Contains(block, "line two") {
		t.Errorf("archived reasoning missing content: %q", block)
	}
	if !strings.Contains(block, "reasoning") {
		t.Errorf("archived block missing label: %q", block)
	}
}

func TestReasoningPanelShowsLastThreeWrappedLines(t *testing.T) {
	m := newTestModel()
	m.width = 80
	if box := m.reasoningBox(); box != "" {
		t.Fatalf("empty reasoning should hide panel: %q", box)
	}
	m.appendReasoning("R1\nR2\nR3\nR4\nR5\nR6\n")
	box := m.reasoningBox()
	rows := strings.Split(box, "\n")
	// The panel is a framed box: three content rows plus the top and
	// bottom border lines.
	if len(rows) != reasoningTailHeight+2 {
		t.Fatalf("panel height = %d, want %d: %q",
			len(rows), reasoningTailHeight+2, box)
	}
	content := rows[1 : len(rows)-1]
	if !strings.Contains(content[0], "…") {
		t.Errorf("truncation marker missing: %q", content[0])
	}
	if strings.Contains(box, "R1") ||
		strings.Contains(box, "R2") ||
		strings.Contains(box, "R3") {
		t.Errorf("old lines leaked into panel: %q", box)
	}
	if !strings.Contains(content[2], "R6") {
		t.Errorf("newest line missing: %q", box)
	}
	// The panel sits directly above the prompt in the fixed view.
	view := m.View()
	if strings.Index(view, "R6") > strings.Index(view, ">") {
		t.Errorf("reasoning panel should be above the prompt: %q", view)
	}
}

func TestWrapByWidth(t *testing.T) {
	long := "wordwithoutspaceswordwithoutspaceswordwithoutspaces"
	lines := wrapByWidth(long, 10)
	if len(lines) != 6 {
		t.Fatalf("long token lines = %d, want 6", len(lines))
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w > 10 {
			t.Errorf("line width %d > 10: %q", w, l)
		}
	}
	// CJK characters are two cells wide and must not be split apart.
	cjk := "明天北京天气怎么样"
	lines = wrapByWidth(cjk, 4)
	if len(lines) != 5 || lines[0] != "明天" || lines[1] != "北京" {
		t.Errorf("cjk wrap = %v", lines)
	}
	joined := strings.Join(lines, "")
	if joined != cjk {
		t.Errorf("cjk wrap lost characters: %q != %q", joined, cjk)
	}
	// Explicit newlines reset the width counter (instead of widening
	// one line) and preserve interior blank lines.
	lines = wrapByWidth("aaaa\nbbbbbbbbbbbb\n\ncc", 4)
	want := []string{"aaaa", "bbbb", "bbbb", "bbbb", "", "cc"}
	if len(lines) != len(want) {
		t.Fatalf("newline wrap = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("newline wrap[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestStreamToolLines(t *testing.T) {
	m := newTestModel()
	m.appendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolCallPart{Call: message.ToolCall{
			Name: "exec_command",
			Arguments: []byte(
				`{"command":"pwd"}`),
		}},
	})
	m.appendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolResultPart{Result: message.ToolResult{
			Content: `{"exit_code":0,"stdout":"/ws\n","stderr":""}`,
		}},
	})
	if len(m.stream.pending) != 3 {
		t.Fatalf("pending = %+v", m.stream.pending)
	}
	if !strings.Contains(m.stream.pending[0], "• Ran pwd") {
		t.Errorf("call line = %q", m.stream.pending[0])
	}
	if !strings.Contains(m.stream.pending[1], "│ /ws") {
		t.Errorf("result line = %q", m.stream.pending[1])
	}
	if !strings.Contains(m.stream.pending[2], "└ ok") {
		t.Errorf("end line = %q", m.stream.pending[2])
	}
}

func TestFoldToolResult(t *testing.T) {
	m := newTestModel()
	m.appendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolCallPart{Call: message.ToolCall{
			ID: "c1", Name: "exec_command",
			Arguments: []byte(
				`{"command":"cat big.go"}`),
		}},
	})
	m.appendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolResultPart{Result: message.ToolResult{
			CallID:  "c1",
			Content: "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10",
		}},
	})
	// header + 2 head + ellipsis + 2 tail + end = 7 lines.
	if len(m.stream.pending) != 7 {
		t.Fatalf("pending = %+v", m.stream.pending)
	}
	joined := strings.Join(m.stream.pending, "\n")
	if !strings.Contains(joined, "+6 lines (Ctrl+T for full output)") {
		t.Errorf("folded result = %q", m.stream.pending)
	}
	// The full block (header + 10 output + end) is kept for Ctrl+T.
	if len(m.transcript.blocks) != 1 || len(m.transcript.blocks[0]) != 12 {
		got := 0
		if len(m.transcript.blocks) > 0 {
			got = len(m.transcript.blocks[0])
		}
		t.Errorf("transcript = %d blocks / %d lines, want 1 block of 12",
			len(m.transcript.blocks), got)
	}
}

func TestAutoFoldToolResultKeepsHeadTail(t *testing.T) {
	m := newTestModel()
	m.appendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolCallPart{Call: message.ToolCall{
			ID: "c1", Name: "exec_command",
			Arguments: []byte(
				`{"command":"cat long.txt"}`),
		}},
	})
	m.appendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolResultPart{Result: message.ToolResult{
			CallID:  "c1",
			Content: "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12",
		}},
	})
	// header + 2 head + ellipsis + 2 tail + end = 7 lines.
	if len(m.stream.pending) != 7 {
		t.Fatalf("pending = %v", m.stream.pending)
	}
	joined := strings.Join(m.stream.pending, "\n")
	if !strings.Contains(joined, "│ line1") ||
		!strings.Contains(joined, "│ line2") {
		t.Errorf("head lines missing: %v", m.stream.pending)
	}
	if !strings.Contains(joined, "+8 lines (Ctrl+T for full output)") {
		t.Errorf("ellipsis missing: %v", m.stream.pending)
	}
	if !strings.Contains(joined, "│ line11") ||
		!strings.Contains(joined, "│ line12") ||
		!strings.Contains(joined, "└ ok") {
		t.Errorf("tail/end missing: %v", m.stream.pending)
	}
	if strings.Contains(joined, "│ line5") {
		t.Errorf("middle leaked through: %v", m.stream.pending)
	}
	if len(m.transcript.blocks) != 1 || len(m.transcript.blocks[0]) != 14 {
		t.Errorf("transcript = %d blocks / %d lines, want 1 block of 14",
			len(m.transcript.blocks), m.transcriptLineCount())
	}
}

func TestToolOutputGroupedPerCall(t *testing.T) {
	m := newTestModel()
	call := func(id, name string) agent.StreamDeltaPayload {
		return agent.StreamDeltaPayload{
			Type: agent.StreamDeltaPart,
			Part: message.ToolCallPart{Call: message.ToolCall{
				ID: id, Name: name,
				Arguments: []byte(`{}`),
			}},
		}
	}
	result := func(id, content string) agent.StreamDeltaPayload {
		return agent.StreamDeltaPayload{
			Type: agent.StreamDeltaPart,
			Part: message.ToolResultPart{Result: message.ToolResult{
				CallID: id, Content: content,
			}},
		}
	}
	// Three parallel calls arrive before any result.
	m.appendDelta(call("c1", "tool_a"))
	m.appendDelta(call("c2", "tool_b"))
	m.appendDelta(call("c3", "tool_c"))
	if len(m.stream.pending) != 0 {
		t.Fatalf("calls should be buffered until results: %v",
			m.stream.pending)
	}
	// Results arrive in completion order; each prints as call+result.
	m.appendDelta(result("c2", "b done"))
	m.appendDelta(result("c1", "a done"))
	m.appendDelta(result("c3", "c done"))
	if len(m.stream.pending) != 9 {
		t.Fatalf("pending = %v", m.stream.pending)
	}
	joined := strings.Join(m.stream.pending, "\n")
	if strings.Index(joined, "tool_b") > strings.Index(joined, "b done") ||
		strings.Index(joined, "tool_a") > strings.Index(joined, "a done") ||
		strings.Index(joined, "tool_c") > strings.Index(joined, "c done") {
		t.Errorf("call/result not paired: %v", m.stream.pending)
	}
}

func TestTranscriptScrollAndExit(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 12
	for i := 1; i <= 8; i++ {
		m.transcript.blocks = append(m.transcript.blocks,
			[]string{fmt.Sprintf("block %d", i)})
	}
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlT})
	next := updated.(*Model)
	if next.mode != modeTranscript {
		t.Fatalf("mode = %v, want transcript", next.mode)
	}
	if !strings.Contains(next.View(), "full output") {
		t.Errorf("view = %q", next.View())
	}
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	next = updated.(*Model)
	if next.transcript.scroll != 1 {
		t.Errorf("scroll = %d, want 1", next.transcript.scroll)
	}
	// Esc returns to the previous mode; the transcript content is kept.
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	next = updated.(*Model)
	if next.mode != modeIdle {
		t.Fatalf("mode = %v, want idle", next.mode)
	}
	if len(next.transcript.blocks) != 8 {
		t.Errorf("transcript blocks dropped: %d", len(next.transcript.blocks))
	}
	// Re-opening restores the overlay.
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyCtrlT})
	next = updated.(*Model)
	if next.mode != modeTranscript {
		t.Fatalf("mode = %v, want transcript again", next.mode)
	}
}

func TestFlushPendingClears(t *testing.T) {
	m := newTestModel()
	m.stream.pending = []string{"a", "b"}
	if cmd := m.flushPending(); cmd == nil {
		t.Fatal("flushPending should return a print command")
	}
	if len(m.stream.pending) != 0 {
		t.Errorf("pending not cleared: %v", m.stream.pending)
	}
	if cmd := m.flushPending(); cmd != nil {
		t.Error("empty pending should return nil")
	}
}

func TestFlushTickIsSingleFlight(t *testing.T) {
	m := newTestModel()
	if m.flushArmed {
		t.Fatal("flush should start unarmed")
	}
	cmd := m.flushTick()
	if cmd == nil {
		t.Fatal("first flushTick should arm and return a tick")
	}
	if !m.flushArmed {
		t.Fatal("flush should be armed after first tick")
	}
	if again := m.flushTick(); again != nil {
		t.Fatal("second flushTick while armed must not schedule another tick")
	}
	// A delivered tick disarms and re-arms the single next cycle.
	_, next := m.Update(flushPrintMsg{})
	if !m.flushArmed {
		t.Fatal("flush should re-arm for the next cycle")
	}
	if next == nil {
		t.Fatal("delivered flush should reschedule the drain tick")
	}
	if again := m.flushTick(); again != nil {
		t.Fatal("rescheduled tick must stay single-flight")
	}
}

func TestBatchMsgDoesNotScheduleFlushTick(t *testing.T) {
	m := newTestModel()
	m.flushArmed = true
	_, cmd := m.Update(batchMsg{})
	if cmd != nil {
		t.Fatalf("batchMsg must not return a tick, got %v", cmd)
	}
}

func TestInteractionSelectCursor(t *testing.T) {
	m := newTestModel()
	replyCh := make(chan interact.Reply, 1)
	updated, _ := m.dispatch(Event{Interact: &InteractEvent{
		Spec: interact.Spec{
			ID: "p1", Kind: interact.KindSelect, Title: "选择方案",
			Options: []interact.Option{
				{Label: "方案 A", Value: "a"},
				{Label: "方案 B", Value: "b"},
			},
		},
		ReplyCh: replyCh,
	}})
	next := updated.(*Model)
	if next.answering.interaction == nil {
		t.Fatal("interaction not active")
	}
	if !strings.Contains(next.View(), "选择方案") {
		t.Error("selector not rendered")
	}
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	next = updated.(*Model)
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(*Model)
	if next.answering.interaction != nil {
		t.Fatal("interaction still active")
	}
	select {
	case reply := <-replyCh:
		if reply.Option == nil || *reply.Option != "b" {
			t.Errorf("reply = %+v", reply)
		}
	default:
		t.Fatal("no reply delivered")
	}
}

func TestInteractionMultiSelect(t *testing.T) {
	m := newTestModel()
	replyCh := make(chan interact.Reply, 1)
	updated, _ := m.dispatch(Event{Interact: &InteractEvent{
		Spec: interact.Spec{
			ID: "p1", Kind: interact.KindSelect, Title: "多选", Multi: true,
			Options: []interact.Option{
				{Label: "A", Value: "a"},
				{Label: "B", Value: "b"},
				{Label: "C", Value: "c"},
			},
		},
		ReplyCh: replyCh,
	}})
	next := updated.(*Model)
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeySpace}, // pick A
		{Type: tea.KeyDown},
		{Type: tea.KeySpace}, // pick B
		{Type: tea.KeyEnter},
	} {
		updated, _ = next.handleKey(key)
		next = updated.(*Model)
	}
	select {
	case reply := <-replyCh:
		if len(reply.Options) != 2 || reply.Options[0] != "a" ||
			reply.Options[1] != "b" {
			t.Errorf("reply = %+v", reply)
		}
	default:
		t.Fatal("no reply delivered")
	}
}

func TestInteractionOtherInput(t *testing.T) {
	m := newTestModel()
	replyCh := make(chan interact.Reply, 1)
	updated, _ := m.dispatch(Event{Interact: &InteractEvent{
		Spec: interact.Spec{
			ID: "p1", Kind: interact.KindSelect, Title: "选一个",
			AllowOther: true,
			Options:    []interact.Option{{Label: "A", Value: "a"}},
		},
		ReplyCh: replyCh,
	}})
	next := updated.(*Model)
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyDown}) // to Other
	next = updated.(*Model)
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(*Model)
	if !next.answering.selOther {
		t.Fatal("other input mode not active")
	}
	next.input.SetValue("我的想法")
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(*Model)
	select {
	case reply := <-replyCh:
		if reply.Text != "我的想法" {
			t.Errorf("reply = %+v", reply)
		}
	default:
		t.Fatal("no reply delivered")
	}
}

func TestInteractionTextSubmit(t *testing.T) {
	m := newTestModel()
	replyCh := make(chan interact.Reply, 1)
	updated, _ := m.dispatch(Event{Interact: &InteractEvent{
		Spec:    interact.Spec{ID: "p1", Kind: interact.KindText, Title: "Which file?"},
		ReplyCh: replyCh,
	}})
	next := updated.(*Model)
	next.input.SetValue("main.go")
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(*Model)
	if next.answering.interaction != nil {
		t.Fatal("interaction still active")
	}
	select {
	case reply := <-replyCh:
		if reply.Text != "main.go" {
			t.Errorf("reply = %+v", reply)
		}
	default:
		t.Fatal("no reply delivered")
	}
}

func TestInteractionConfirmKeys(t *testing.T) {
	m := newTestModel()
	replyCh := make(chan interact.Reply, 1)
	updated, _ := m.dispatch(Event{Interact: &InteractEvent{
		Spec:    interact.Spec{ID: "p1", Kind: interact.KindConfirm, Title: "允许?"},
		ReplyCh: replyCh,
	}})
	next := updated.(*Model)
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	next = updated.(*Model)
	select {
	case reply := <-replyCh:
		if reply.Option == nil || *reply.Option != "yes" {
			t.Errorf("reply = %+v", reply)
		}
	default:
		t.Fatal("no reply delivered")
	}
}

func TestInteractionCancel(t *testing.T) {
	m := newTestModel()
	replyCh := make(chan interact.Reply, 1)
	updated, _ := m.dispatch(Event{Interact: &InteractEvent{
		Spec:    interact.Spec{ID: "p1", Kind: interact.KindText, Title: "q"},
		ReplyCh: replyCh,
	}})
	next := updated.(*Model)
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	next = updated.(*Model)
	if next.answering.interaction != nil {
		t.Fatal("interaction still active")
	}
	select {
	case reply := <-replyCh:
		if reply.Status != interact.ReplyCancelled {
			t.Errorf("reply = %+v", reply)
		}
	default:
		t.Fatal("no reply delivered")
	}
}

func TestBridgeAskTimesOutWithoutUI(t *testing.T) {
	b := NewBridge(16)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := b.Ask(ctx, interact.Spec{ID: "p1"}); err == nil {
		t.Fatal("Ask without a consuming UI should time out")
	}
}

func textDelta(content string) agent.StreamDeltaPayload {
	return agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.TextPart{Text: content},
	}
}
