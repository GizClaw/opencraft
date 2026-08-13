package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

func newTestModel() *Model {
	return New(nil, Options{}, NewBridge(16))
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

func TestViewRenders(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 24
	m.view.Width, m.view.Height = 80, 18
	m.input.SetWidth(80)
	m.chat.AppendUser("hello")
	m.refresh()
	v := m.View()
	if !strings.Contains(v, "hello") || !strings.Contains(v, "opencraft") {
		t.Errorf("view = %q", v)
	}
}

func TestAppendDelta(t *testing.T) {
	m := newTestModel()
	m.chat.AppendAssistant()
	m.chat.AppendDelta(textDelta("hi"))
	m.chat.AppendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolCallPart{Call: message.ToolCall{
			ID: "call-1", Name: "exec_command",
		}},
	})
	if m.chat.blocks[0].text != "hi" {
		t.Errorf("assistant text = %q", m.chat.blocks[0].text)
	}
	if len(m.chat.blocks) != 2 || m.chat.blocks[1].kind != blockTool ||
		m.chat.blocks[1].name != "exec_command" {
		t.Errorf("blocks = %+v", m.chat.blocks)
	}
}

func TestAppendDeltaInterleavesAssistantBlocks(t *testing.T) {
	m := newTestModel()
	m.chat.AppendUser("hello")
	m.chat.AppendAssistant()
	m.chat.AppendDelta(textDelta("let me check"))
	m.chat.AppendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ToolCallPart{Call: message.ToolCall{
			ID: "call-1", Name: "exec_command",
		}},
	})
	m.chat.AppendDelta(textDelta("found it"))

	if len(m.chat.blocks) != 4 {
		t.Fatalf("blocks = %d, want user + assistant + tool + assistant: %+v",
			len(m.chat.blocks), m.chat.blocks)
	}
	if m.chat.blocks[1].kind != blockAssistant ||
		m.chat.blocks[1].text != "let me check" {
		t.Errorf("first assistant block = %+v", m.chat.blocks[1])
	}
	if m.chat.blocks[2].kind != blockTool {
		t.Errorf("third block = %+v, want tool", m.chat.blocks[2])
	}
	if m.chat.blocks[3].kind != blockAssistant ||
		m.chat.blocks[3].text != "found it" {
		t.Errorf("blocks = %+v, want a fresh assistant block after the tool", m.chat.blocks)
	}
}

func TestAppendDeltaStreamsReasoningInOrder(t *testing.T) {
	m := newTestModel()
	m.chat.AppendUser("hello")
	m.chat.AppendDelta(agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.ReasoningPart{Text: "think"},
	})
	m.chat.AppendDelta(textDelta("answer"))

	want := []struct {
		kind blockKind
		text string
	}{
		{blockUser, "hello"},
		{blockReasoning, "think"},
		{blockAssistant, "answer"},
	}
	if len(m.chat.blocks) != len(want) {
		t.Fatalf("blocks = %d, want %d: %+v", len(m.chat.blocks), len(want), m.chat.blocks)
	}
	for i, w := range want {
		b := m.chat.blocks[i]
		if b.kind != w.kind || b.text != w.text {
			t.Errorf("block[%d] = %+v, want kind %v text %q", i, b, w.kind, w.text)
		}
	}
}

func TestFormatExecResult(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		stdout string
		stderr string
		want   string
	}{
		{"empty output", 0, "", "", "exit_code=0"},
		{"stdout only", 0, "out", "", "exit_code=0\n[stdout]\nout"},
		{"stderr only", 1, "", "err", "exit_code=1\n[stderr]\nerr"},
		{"both", 2, "out", "err",
			"exit_code=2\n[stderr]\nerr\n[stdout]\nout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatExecResult(tc.code, tc.stdout, tc.stderr); got != tc.want {
				t.Errorf("formatExecResult = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestKeyRunesUpdateInput(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("hello"),
	})
	next := updated.(*Model)
	if next.input.Value() != "hello" {
		t.Fatalf("input value = %q, want hello", next.input.Value())
	}
}

func TestEnterSubmitsText(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("hello")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(*Model)
	if len(next.chat.blocks) < 1 || next.chat.blocks[0].text != "hello" {
		t.Errorf("blocks = %+v", next.chat.blocks)
	}
	if cmd == nil {
		t.Fatal("enter should return a submit command")
	}
	if next.input.Value() != "" {
		t.Errorf("input not reset: %q", next.input.Value())
	}
}

func TestAskModalReplies(t *testing.T) {
	m := newTestModel()
	replyCh := make(chan agent.UserReply, 1)
	updated, _ := m.dispatch(Event{Prompt: &PromptRequest{
		Text: "Which file?", ReplyCh: replyCh,
	}})
	next := updated.(*Model)
	if next.modal == nil || next.modal.Kind() != modalAsk {
		t.Fatal("ask modal not active")
	}
	next.width = 80
	if !strings.Contains(next.View(), "Which file?") {
		t.Error("question not rendered")
	}

	next.modal.Input().SetValue("main.go")
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(*Model)
	if next.modal != nil {
		t.Fatal("ask modal still active")
	}
	select {
	case reply := <-replyCh:
		if len(reply.Parts) != 1 ||
			reply.Parts[0].(message.TextPart).Text != "main.go" {
			t.Errorf("reply = %+v", reply)
		}
	default:
		t.Fatal("no reply delivered")
	}
}

func TestApproveModal(t *testing.T) {
	m := newTestModel()
	done := make(chan bool, 1)
	updated, _ := m.dispatch(Event{Approve: &ApproveRequest{
		Call: message.ToolCall{Name: "exec_command"},
		Done: done,
	}})
	next := updated.(*Model)
	if next.modal == nil || next.modal.Kind() != modalApprove {
		t.Fatal("approve modal not active")
	}
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	next = updated.(*Model)
	if next.modal != nil {
		t.Fatal("approve modal still active")
	}
	select {
	case approved := <-done:
		if !approved {
			t.Error("expected approval")
		}
	default:
		t.Fatal("no approval delivered")
	}
}

func TestApproveModalReject(t *testing.T) {
	m := newTestModel()
	done := make(chan bool, 1)
	updated, _ := m.dispatch(Event{Approve: &ApproveRequest{
		Call: message.ToolCall{Name: "exec_command"},
		Done: done,
	}})
	next := updated.(*Model)
	updated, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	next = updated.(*Model)
	if next.modal != nil {
		t.Fatal("approve modal still active")
	}
	select {
	case approved := <-done:
		if approved {
			t.Error("expected rejection")
		}
	default:
		t.Fatal("no rejection delivered")
	}
}

func TestBridgeAskUserTimesOutWithoutUI(t *testing.T) {
	b := NewBridge(16)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := b.AskUser(ctx, agent.UserPrompt{}); err == nil {
		t.Fatal("AskUser without a consuming UI should time out")
	}
}

func textDelta(content string) agent.StreamDeltaPayload {
	return agent.StreamDeltaPayload{
		Type: agent.StreamDeltaPart,
		Part: message.TextPart{Text: content},
	}
}
