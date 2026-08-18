// Package tui implements the opencraft terminal UI (bubbletea): an
// interactive prompt with streaming output and tool-call rendering.
// It runs in-process and drives the runtime through Go APIs.
package tui

import (
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GizClaw/opencraft/internal/runtime"
	"github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/skills"
)

// Options configures the TUI.
type Options struct {
	// ContextID is the session context used for every turn (default
	// "tui").
	ContextID string
	// ConfigDir is the user configuration directory
	// (~/.opencraft/config) where TUI preferences (tui.yaml) are
	// persisted. Empty disables persistence.
	ConfigDir string
	// Model is the initially configured model ("provider/name") shown
	// in the header until the first usage report arrives.
	Model string
	// Version is the opencraft build version shown in the startup
	// brand block. Empty hides the version badge.
	Version string
	// Sessions is the project conversation store used by /resume.
	Sessions *sessions.Store
	// WorkDir is the workspace root apply_patch renders diffs against
	// (real file line numbers). Empty disables file-based numbering.
	WorkDir string
	// Skills is the discovered skills registry backing /skills. Nil
	// hides the picker (skills not wired).
	Skills *skills.Service
}

// Run starts the TUI and blocks until it exits.
func Run(
	rtc *runtime.Controller,
	opts Options,
	bridge *Bridge,
	broker *runtime.Broker,
) error {
	if opts.ContextID == "" {
		opts.ContextID = "tui"
	}
	m := New(rtc, opts, bridge, broker)
	// Initialize the color profile before tea takes over the terminal,
	// otherwise lipgloss re-queries the background color while the UI is
	// running and the terminal response arrives as keyboard input.
	_ = lipgloss.HasDarkBackground()
	// Mouse cell motion is enabled for the transcript viewport's wheel
	// scrolling (the whole screen is now managed by the app, so the
	// native scrollback no longer needs mouse access). Clicks are
	// ignored: the UI stays keyboard-driven (Ctrl+T folding, ↑/↓
	// pickers). Note that mouse reporting disables the terminal's
	// native selection everywhere — including the composer input —
	// so Ctrl+E in the TUI toggles capture off/on.
	// Enable the kitty keyboard protocol via vtinput so modified keys
	// (Shift+Enter/Option+Enter for newline, disambiguated Esc,
	// Ctrl+letter) arrive as distinct events; unsupported terminals
	// fall back to legacy sequences, which vtinput parses too.
	ki, err := enableKittyInput()
	if err != nil {
		// Not a terminal (e.g. piped stdin): fall back to bubbletea's
		// default input handling, which opens a TTY when needed.
		p := tea.NewProgram(m)
		m.program = p
		go bridgeLoop(p, bridge)
		_, err := p.Run()
		return err
	}
	defer ki.close()

	// bubbletea reads from a pipe that never produces data; real input
	// events are forwarded from vtinput via Program.Send. The alternate
	// screen gives bubbletea a full frame to manage, so resizes repaint
	// cleanly instead of fighting the terminal scrollback.
	p := tea.NewProgram(m,
		tea.WithInput(ki.pipeReader),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion())
	m.program = p
	go inputLoop(p, ki.events)
	// Drain the bridge on a dedicated goroutine and deliver batches via
	// Program.Send. A blocking waitEvents Cmd would freeze bubbletea's
	// event loop: execBatchMsg waits for every command in a batch, so a
	// long gap between stream events would stall the whole UI.
	go bridgeLoop(p, bridge)
	_, err = p.Run()
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
