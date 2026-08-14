// Package tui implements the opencraft terminal UI (bubbletea): an
// interactive prompt with streaming output and tool-call rendering.
// It runs in-process and drives the runtime through Go APIs.
package tui

import (
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/interact"
	"github.com/GizClaw/opencraft/internal/sessions"
)

// Options configures the TUI.
type Options struct {
	// ContextID is the session context used for every turn (default
	// "tui").
	ContextID string
	// Model is the initially configured model ("provider/name") shown
	// in the header until the first usage report arrives.
	Model string
	// Sessions is the project conversation store used by /resume.
	Sessions *sessions.Store
}

// Run starts the TUI and blocks until it exits.
func Run(
	rtc *app.RuntimeController,
	opts Options,
	bridge *Bridge,
	broker *interact.Broker,
) error {
	if opts.ContextID == "" {
		opts.ContextID = "tui"
	}
	m := New(rtc, opts, bridge, broker)
	// Initialize the color profile before tea takes over the terminal,
	// otherwise lipgloss re-queries the background color while the UI is
	// running and the terminal response arrives as keyboard input.
	_ = lipgloss.HasDarkBackground()
	// No mouse tracking: the UI is keyboard-driven (Ctrl+O folding,
	// ↑/↓ pickers), and the agent transcript lives in the native
	// terminal scrollback. Capturing mouse events would swallow text
	// selection and wheel scrolling, so leave them to the terminal.
	p := tea.NewProgram(m)
	m.program = p
	// Drain the bridge on a dedicated goroutine and deliver batches via
	// Program.Send. A blocking waitEvents Cmd would freeze bubbletea's
	// event loop: execBatchMsg waits for every command in a batch, so a
	// long gap between stream events would stall the whole UI.
	go bridgeLoop(p, bridge)
	_, err := p.Run()
	return err
}

// bridgeLoop drains the bridge event channel into batches and sends
// them to the program. It exits when the bridge channel closes.
func bridgeLoop(p *tea.Program, bridge *Bridge) {
	for {
		first, ok := <-bridge.Events()
		if !ok {
			return
		}
		events := []Event{first}
	drain:
		for {
			select {
			case ev := <-bridge.Events():
				events = append(events, ev)
			default:
				break drain
			}
		}
		p.Send(batchMsg{events: events})
	}
}
