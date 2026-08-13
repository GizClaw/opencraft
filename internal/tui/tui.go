// Package tui implements the opencraft terminal UI (bubbletea): an
// interactive prompt with streaming output and tool-call rendering.
// It runs in-process and drives the runtime through Go APIs.
package tui

import (
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/GizClaw/opencraft/internal/app"
)

// Options configures the TUI.
type Options struct {
	// ContextID is the session context used for every turn (default
	// "tui").
	ContextID string
}

// Run starts the TUI and blocks until it exits.
func Run(rtc *app.RuntimeController, opts Options, bridge *Bridge) error {
	if opts.ContextID == "" {
		opts.ContextID = "tui"
	}
	m := New(rtc, opts, bridge)
	// Initialize the color profile before tea takes over the terminal,
	// otherwise lipgloss re-queries the background color while the UI is
	// running and the terminal response arrives as keyboard input.
	_ = lipgloss.HasDarkBackground()
	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	m.program = p
	_, err := p.Run()
	return err
}
