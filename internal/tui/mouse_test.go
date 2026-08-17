package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestMouseCaptureToggle verifies Ctrl+E releases the terminal mouse
// so the native selection works again (composer input included), and
// restores it for transcript drag selection.
func TestMouseCaptureToggle(t *testing.T) {
	m := newTestModel()
	if !m.mouseCapture {
		t.Fatal("mouse capture should default to on")
	}

	// Ctrl+E releases the mouse so the terminal's native selection
	// works again (composer input included).
	next, cmd := m.handleIdleKey(tea.KeyMsg{
		Type: tea.KeyCtrlE,
	})
	m = next.(*Model)
	if m.mouseCapture {
		t.Fatal("ctrl+e should disable mouse capture")
	}
	if cmd == nil {
		t.Fatal("ctrl+e should return a mouse-mode command")
	}
	if !strings.Contains(m.footerLine(), "native select") {
		t.Errorf("footer should hint native selection when capture is off: %q",
			m.footerLine())
	}

	// With capture off, mouse events must not start a drag selection.
	m.handleMouse(tea.MouseMsg{
		X: 5, Y: 0, Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	if m.selection.active {
		t.Fatal("mouse selection must be disabled while capture is off")
	}

	// Toggle back on restores transcript drag selection.
	next, cmd = m.handleIdleKey(tea.KeyMsg{Type: tea.KeyCtrlE})
	m = next.(*Model)
	if !m.mouseCapture {
		t.Fatal("second ctrl+e should re-enable mouse capture")
	}
	if cmd == nil {
		t.Fatal("second ctrl+e should return a mouse-mode command")
	}
}
