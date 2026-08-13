package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	sessions "github.com/GizClaw/flowcraft/core/runtime/session"

	"github.com/GizClaw/opencraft/internal/app"
)

// Messages.
type turnStartedMsg struct {
	lease *sessions.Lease
	turn  *sessions.Turn
}

type turnDoneMsg struct {
	err error
}

type renderFlushMsg struct{}

// Model is the top-level router: it owns the bridge, the chat state,
// the current modal, and the shared chrome (header/status/input).
// View-specific logic lives in ChatState / Modal.
type Model struct {
	rtc     *app.RuntimeController
	opts    Options
	ctx     context.Context
	program *tea.Program
	bridge  *Bridge
	chat    ChatState
	modal   *Modal

	input   textarea.Model
	view    viewport.Model
	spinner spinner.Model

	busy       bool
	lease      *sessions.Lease
	turn       *sessions.Turn
	status     string
	lastRender time.Time

	width  int
	height int
}

// New creates the top-level model over the bridge.
func New(rtc *app.RuntimeController, opts Options, bridge *Bridge) *Model {
	input := textarea.New()
	input.Placeholder = "Ask opencraft… (Enter to send, Ctrl+C to interrupt/quit)"
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.Focus()

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return &Model{
		rtc:     rtc,
		opts:    opts,
		ctx:     context.Background(),
		bridge:  bridge,
		input:   input,
		view:    viewport.New(80, 20),
		spinner: spin,
	}
}

func (m *Model) Init() tea.Cmd {
	m.bridge.Start()
	return tea.Batch(textarea.Blink, spinner.Tick, m.waitEvents())
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		headerH := lipgloss.Height(m.header())
		inputH := min(5, max(3, m.height/6))
		inputTotal := inputH + 2 // rounded border
		m.view.Width = msg.Width
		m.view.Height = max(0, msg.Height-headerH-1-inputTotal)
		m.input.SetWidth(msg.Width)
		m.input.SetHeight(inputH)
		m.view.SetContent(m.chat.Render())
		return m, nil

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.busy {
			m.refresh()
			return m, cmd
		}
		return m, cmd

	case batchMsg:
		// The event loop is a Cmd chain: re-arm waitEvents after every
		// batch so the bridge keeps draining.
		for _, ev := range msg.events {
			m.applyEvent(ev)
		}
		m.refresh()
		m.view.GotoBottom()
		return m, tea.Batch(m.waitEvents(), m.renderTick())

	case renderFlushMsg:
		m.lastRender = time.Now()
		m.refresh()
		m.view.GotoBottom()
		return m, nil

	case turnStartedMsg:
		m.busy = true
		m.lease = msg.lease
		m.turn = msg.turn
		m.status = "working"
		m.refresh()
		return m, tea.Batch(m.waitCmd(), spinner.Tick)

	case turnDoneMsg:
		m.busy = false
		m.chat.AppendError(msg.err)
		if m.lease != nil {
			_ = m.lease.Close()
			m.lease = nil
		}
		m.turn = nil
		m.status = ""
		m.refresh()
		m.view.GotoBottom()
		return m, nil

	case tea.QuitMsg:
		if m.lease != nil {
			_ = m.lease.Close()
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) dispatch(ev Event) (tea.Model, tea.Cmd) {
	m.applyEvent(ev)
	return m, nil
}

func (m *Model) applyEvent(ev Event) {
	switch {
	case ev.Stream != nil:
		m.chat.AppendDelta(ev.Stream.Delta)
	case ev.Prompt != nil:
		m.modal = NewAskModal(ev.Prompt.Text, ev.Prompt.ReplyCh)
	case ev.Approve != nil:
		m.modal = NewApproveModal(ev.Approve.Call, ev.Approve.Done)
	case ev.Status != nil:
		m.status = ev.Status.Text
		m.busy = ev.Status.Busy
	}
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modal != nil {
		return m.handleModalKey(msg)
	}
	switch msg.String() {
	case "ctrl+c":
		if m.busy && m.turn != nil {
			_ = m.turn.Interrupt(agent.Interrupt{Cause: agent.CauseUserInput})
			m.status = "interrupted"
			return m, nil
		}
		return m, tea.Quit
	case "esc":
		if m.busy && m.turn != nil {
			_ = m.turn.Interrupt(agent.Interrupt{Cause: agent.CauseUserInput})
			return m, nil
		}
	case "enter":
		if m.busy {
			return m, nil
		}
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		m.input.Reset()
		m.chat.AppendUser(text)
		m.chat.AppendAssistant()
		m.refresh()
		return m, m.startTurnCmd(text)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	modal := m.modal
	switch msg.String() {
	case "enter":
		if modal.Kind() == modalAsk {
			reply := strings.TrimSpace(modal.Input().Value())
			m.modal = nil
			modal.ReplyCh() <- agent.UserReply{
				Parts: []message.Part{message.TextPart{Text: reply}},
			}
			m.chat.AppendReply(reply)
			m.refresh()
			m.view.GotoBottom()
		} else {
			m.modal = nil
			modal.DoneCh() <- true
			m.refresh()
		}
	case "y":
		if modal.Kind() == modalApprove || modal.Kind() == modalConfirm {
			m.modal = nil
			modal.DoneCh() <- true
			m.refresh()
		}
	case "n", "esc":
		if modal.Kind() == modalApprove || modal.Kind() == modalConfirm {
			m.modal = nil
			modal.DoneCh() <- false
			m.refresh()
		}
	case "ctrl+c":
		m.modal = nil
		if modal.Kind() == modalAsk {
			modal.ReplyCh() <- agent.UserReply{}
		} else {
			modal.DoneCh() <- false
		}
		m.status = "cancelled"
		m.refresh()
	default:
		modal.Update(msg)
	}
	return m, nil
}

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
	turn := m.turn
	return func() tea.Msg {
		_, err := turn.Wait(m.ctx)
		return turnDoneMsg{err: err}
	}
}

// waitEvents drains the bridge event channel into the UI.
func (m *Model) waitEvents() tea.Cmd {
	return func() tea.Msg {
		first := <-m.bridge.Events()
		events := []Event{first}
		// Drain whatever is already queued so one Update consumes the
		// whole burst; the bridge channel then never fills and the
		// session's sink never blocks.
		for {
			select {
			case ev := <-m.bridge.Events():
				events = append(events, ev)
			default:
				return batchMsg{events: events}
			}
		}
	}
}

func (m *Model) refresh() {
	m.view.SetContent(m.chat.Render())
}

// renderTick throttles viewport repaints while tokens stream in.
func (m *Model) renderTick() tea.Cmd {
	const interval = 40 * time.Millisecond
	now := time.Now()
	if now.Sub(m.lastRender) >= interval {
		m.lastRender = now
		m.refresh()
		m.view.GotoBottom()
		return nil
	}
	wait := interval - now.Sub(m.lastRender)
	return tea.Tick(wait, func(time.Time) tea.Msg { return renderFlushMsg{} })
}

func (m *Model) render() string {
	if m.modal != nil {
		return m.renderModal()
	}
	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.header(),
		m.view.View(),
		m.statusBar(),
		inputStyle.Render(m.input.View()),
	)
}

func (m *Model) renderModal() string {
	question := m.modal.View(m.width)
	var input string
	if m.modal.Kind() == modalAsk {
		input = inputStyle.Render(m.modal.Input().View())
	}
	parts := []string{m.header(), question}
	if input != "" {
		parts = append(parts, input)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *Model) header() string {
	right := "opencraft"
	if m.busy {
		right = m.spinner.View() + " working"
	}
	return headerStyle.Width(m.width).Render(" opencraft  " + right)
}

func (m *Model) statusBar() string {
	left := "Enter send · Ctrl+C interrupt/quit"
	if m.status != "" {
		left = m.status
	}
	return statusStyle.Width(m.width).Render(left)
}

// View implements tea.Model.
func (m *Model) View() string {
	return m.render()
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
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
