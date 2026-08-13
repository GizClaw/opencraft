package tui

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// modalKind discriminates modal types.
type modalKind int

const (
	modalAsk modalKind = iota
	modalApprove
	modalConfirm
)

// Modal is the unified overlay for ask_user / approval / confirm.
// Rendering only; key routing happens in the top-level model which
// delivers results through the channels.
type Modal struct {
	kind    modalKind
	text    string
	call    *message.ToolCall
	input   *textarea.Model
	replyCh chan agent.UserReply
	doneCh  chan bool
}

// NewAskModal creates an ask_user modal.
func NewAskModal(text string, replyCh chan agent.UserReply) *Modal {
	input := textarea.New()
	input.Placeholder = "Type your answer…"
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.Focus()
	return &Modal{
		kind: modalAsk, text: text, input: &input, replyCh: replyCh,
	}
}

// NewApproveModal creates an approval modal.
func NewApproveModal(call message.ToolCall, doneCh chan bool) *Modal {
	return &Modal{
		kind: modalApprove, call: &call, doneCh: doneCh,
	}
}

// NewConfirmModal creates a generic confirm modal.
func NewConfirmModal(text string, doneCh chan bool) *Modal {
	return &Modal{kind: modalConfirm, text: text, doneCh: doneCh}
}

// Kind returns the modal kind.
func (m *Modal) Kind() modalKind { return m.kind }

// Input returns the ask input (ask modal only).
func (m *Modal) Input() *textarea.Model { return m.input }

// ReplyCh returns the ask reply channel.
func (m *Modal) ReplyCh() chan agent.UserReply { return m.replyCh }

// DoneCh returns the approve/confirm result channel.
func (m *Modal) DoneCh() chan bool { return m.doneCh }

// Update forwards keys to the ask input.
func (m *Modal) Update(msg tea.KeyMsg) {
	if m.input != nil {
		updated, _ := m.input.Update(msg)
		m.input = &updated
	}
}

// View renders the modal box.
func (m *Modal) View(width int) string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 2).
		Width(max(10, width-4))

	var title, body string
	switch m.kind {
	case modalAsk:
		title = "? " + m.text
		body = "answer the question · Enter to send · Ctrl+C to cancel"
	case modalApprove:
		title = "⚠ approve: " + m.call.Name
		body = "  " + truncate(string(m.call.Arguments), 400)
		body += "\n\n  y/Enter approve · n/Esc reject · Ctrl+C cancel"
	case modalConfirm:
		title = "? " + m.text
		body = "  y/Enter confirm · n/Esc cancel"
	}
	return style.Render(title + "\n\n" + body)
}
