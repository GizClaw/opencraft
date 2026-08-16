package tui

import (
	"io"
	"os"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbletea"
	"github.com/unxed/vtinput"
	"golang.org/x/term"
)

// Keyboard input goes through vtinput instead of bubbletea's own
// parser: the kitty keyboard protocol (CSI-u) reports modified keys —
// Shift+Enter, disambiguated Esc, Ctrl+letter — as distinct events
// instead of collapsing them into ambiguous bytes (Ctrl+C would
// otherwise arrive as "\x1b[99;5u" and be dropped). vtinput parses
// both kitty and legacy fallback sequences and yields one InputEvent
// per keypress. This file pushes the protocol, then maps those events
// onto the KeyMsgs the UI already understands.
//
// The protocol is pushed with flags 1|2|4 (disambiguate escape codes,
// report event types, report alternate keys) — the same set codex-rs
// pushes. Flag 8 ("report all keys as escape codes") is deliberately
// NOT set: terminals then encode IME-committed text as CSI-u events,
// which breaks Chinese/Japanese input on several terminals (iTerm2
// included). Without flag 8, plain text — including IME commits —
// arrives as ordinary UTF-8, which vtinput parses as-is.
//
// The key choices mirror codex-rs/crossterm:
//   - Shift+Enter and Option+Enter insert a newline, plain Enter
//     submits;
//   - Ctrl+Enter is dropped;
//   - Ctrl+letter maps to the matching C0 control key (Ctrl+C quits);
//   - Esc is Esc.
func mapInputEvent(ev *vtinput.InputEvent) (tea.KeyMsg, bool) {
	if ev == nil || ev.Type != vtinput.KeyEventType || !ev.KeyDown {
		return tea.KeyMsg{}, false
	}

	cks := ev.ControlKeyState
	ctrl := cks.Contains(vtinput.LeftCtrlPressed) || cks.Contains(vtinput.RightCtrlPressed)
	shift := cks.Contains(vtinput.ShiftPressed)
	alt := cks.Contains(vtinput.LeftAltPressed) || cks.Contains(vtinput.RightAltPressed)

	// Ctrl+A..Ctrl+Z are the C0 control codes bubbletea already models
	// as KeyCtrlA..KeyCtrlZ. This is what keeps Ctrl+C/Ctrl+T working
	// when the kitty protocol reports them as CSI-u events instead of
	// the legacy control bytes.
	if ctrl && !alt && ev.VirtualKeyCode >= vtinput.VK_A && ev.VirtualKeyCode <= vtinput.VK_Z {
		return tea.KeyMsg{
			Type: tea.KeyType(int(tea.KeyCtrlA) + int(ev.VirtualKeyCode-vtinput.VK_A)),
		}, true
	}

	switch ev.VirtualKeyCode {
	case vtinput.VK_RETURN:
		switch {
		case (shift || alt) && !ctrl:
			// Shift+Enter / Option+Enter newline (codex-rs binds both).
			return tea.KeyMsg{Type: tea.KeyCtrlJ}, true
		case ctrl:
			return tea.KeyMsg{}, false // Ctrl+Enter: intentionally dropped
		default:
			return tea.KeyMsg{Type: tea.KeyEnter}, true
		}
	case vtinput.VK_ESCAPE:
		if ctrl || shift || alt {
			return tea.KeyMsg{}, false
		}
		return tea.KeyMsg{Type: tea.KeyEsc}, true
	case vtinput.VK_TAB:
		switch {
		case shift:
			return tea.KeyMsg{Type: tea.KeyShiftTab}, true
		case ctrl || alt:
			return tea.KeyMsg{}, false
		default:
			return tea.KeyMsg{Type: tea.KeyTab}, true
		}
	case vtinput.VK_BACK:
		return tea.KeyMsg{Type: tea.KeyBackspace, Alt: alt}, true
	case vtinput.VK_DELETE:
		return tea.KeyMsg{Type: tea.KeyDelete, Alt: alt}, true
	case vtinput.VK_INSERT:
		return tea.KeyMsg{Type: tea.KeyInsert}, true
	case vtinput.VK_HOME:
		return tea.KeyMsg{Type: homeEndKey(tea.KeyHome, ctrl, shift, alt), Alt: alt}, true
	case vtinput.VK_END:
		return tea.KeyMsg{Type: homeEndKey(tea.KeyEnd, ctrl, shift, alt), Alt: alt}, true
	case vtinput.VK_PRIOR:
		if ctrl {
			return tea.KeyMsg{Type: tea.KeyCtrlPgUp}, true
		}
		return tea.KeyMsg{Type: tea.KeyPgUp, Alt: alt}, true
	case vtinput.VK_NEXT:
		if ctrl {
			return tea.KeyMsg{Type: tea.KeyCtrlPgDown}, true
		}
		return tea.KeyMsg{Type: tea.KeyPgDown, Alt: alt}, true
	case vtinput.VK_UP:
		return tea.KeyMsg{Type: arrowKey(tea.KeyUp, ctrl, shift, alt), Alt: alt}, true
	case vtinput.VK_DOWN:
		return tea.KeyMsg{Type: arrowKey(tea.KeyDown, ctrl, shift, alt), Alt: alt}, true
	case vtinput.VK_LEFT:
		return tea.KeyMsg{Type: arrowKey(tea.KeyLeft, ctrl, shift, alt), Alt: alt}, true
	case vtinput.VK_RIGHT:
		return tea.KeyMsg{Type: arrowKey(tea.KeyRight, ctrl, shift, alt), Alt: alt}, true
	case vtinput.VK_SPACE:
		if ctrl || alt {
			return tea.KeyMsg{}, false
		}
		return tea.KeyMsg{Type: tea.KeySpace}, true
	}

	if ev.Char == 0 {
		return tea.KeyMsg{}, false
	}
	r := ev.Char
	// vtinput uppercases Alt-modified characters to mirror legacy
	// terminals; lowercase them so bindings like "alt+f" keep matching.
	if alt && !shift {
		r = unicode.ToLower(r)
	}
	// Terminals that report the kitty event without the alternate-key
	// codepoint send the unshifted character, so restore the case that
	// Shift should have produced.
	if shift && !alt && r >= 'a' && r <= 'z' {
		r = unicode.ToUpper(r)
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: alt}, true
}

// kittyEnable pushes the kitty keyboard protocol with flags 1|2|4.
// kittyDisable pops that single pushed level on exit.
const (
	kittyEnable  = "\x1b[>7u"
	kittyDisable = "\x1b[<1u"
)

// arrowKey selects the bubbletea key type for an arrow key with
// modifiers. bubbletea has dedicated types for the common Ctrl/Shift
// combos; Alt is carried on the key's Alt flag (there is no KeyAltUp).
func arrowKey(base tea.KeyType, ctrl, shift, alt bool) tea.KeyType {
	switch {
	case ctrl && shift:
		switch base {
		case tea.KeyUp:
			return tea.KeyCtrlShiftUp
		case tea.KeyDown:
			return tea.KeyCtrlShiftDown
		case tea.KeyLeft:
			return tea.KeyCtrlShiftLeft
		case tea.KeyRight:
			return tea.KeyCtrlShiftRight
		}
	case ctrl:
		switch base {
		case tea.KeyUp:
			return tea.KeyCtrlUp
		case tea.KeyDown:
			return tea.KeyCtrlDown
		case tea.KeyLeft:
			return tea.KeyCtrlLeft
		case tea.KeyRight:
			return tea.KeyCtrlRight
		}
	case shift:
		switch base {
		case tea.KeyUp:
			return tea.KeyShiftUp
		case tea.KeyDown:
			return tea.KeyShiftDown
		case tea.KeyLeft:
			return tea.KeyShiftLeft
		case tea.KeyRight:
			return tea.KeyShiftRight
		}
	}
	return base
}

func homeEndKey(base tea.KeyType, ctrl, shift, alt bool) tea.KeyType {
	switch {
	case ctrl && shift:
		if base == tea.KeyHome {
			return tea.KeyCtrlShiftHome
		}
		return tea.KeyCtrlShiftEnd
	case ctrl:
		if base == tea.KeyHome {
			return tea.KeyCtrlHome
		}
		return tea.KeyCtrlEnd
	case shift:
		if base == tea.KeyHome {
			return tea.KeyShiftHome
		}
		return tea.KeyShiftEnd
	}
	return base
}

// inputLoop forwards parsed input events to the program. Key events
// become KeyMsgs; mouse wheel events become MouseMsgs so the
// transcript viewport can scroll. It exits when the vtinput event
// channel closes (reader shutdown).
func inputLoop(p *tea.Program, events <-chan *vtinput.InputEvent) {
	for ev := range events {
		if msg, ok := mapInputEvent(ev); ok {
			p.Send(msg)
			continue
		}
		if msg, ok := mapMouseEvent(ev); ok {
			p.Send(msg)
		}
	}
}

// mapMouseEvent maps a vtinput mouse event onto the bubbletea MouseMsg
// the UI understands: wheel events scroll the viewport, and left
// button press/motion/release drive drag selection. Other buttons and
// plain motion are dropped.
func mapMouseEvent(ev *vtinput.InputEvent) (tea.MouseMsg, bool) {
	if ev == nil || ev.Type != vtinput.MouseEventType {
		return tea.MouseMsg{}, false
	}
	msg := tea.MouseMsg{X: int(ev.MouseX), Y: int(ev.MouseY)}
	if ev.WheelDirection != 0 {
		if ev.WheelDirection > 0 {
			msg.Button = tea.MouseButtonWheelUp
		} else {
			msg.Button = tea.MouseButtonWheelDown
		}
		msg.Action = tea.MouseActionPress
		return msg, true
	}
	if ev.MouseEventFlags&vtinput.MouseMoved != 0 {
		msg.Button = tea.MouseButtonLeft
		msg.Action = tea.MouseActionMotion
		return msg, true
	}
	if ev.ButtonState&vtinput.FromLeft1stButtonPressed != 0 {
		msg.Button = tea.MouseButtonLeft
		if ev.KeyDown {
			msg.Action = tea.MouseActionPress
		} else {
			msg.Action = tea.MouseActionRelease
		}
		return msg, true
	}
	if !ev.KeyDown {
		// Release events can arrive with an empty button state; treat
		// them as a left-button release so the drag always ends.
		msg.Button = tea.MouseButtonLeft
		msg.Action = tea.MouseActionRelease
		return msg, true
	}
	return tea.MouseMsg{}, false
}

// kittyInput owns the terminal state vtinput changed and the event
// channel it parses. bubbletea is given a separate pipe so it never
// reads stdin itself — vtinput owns the real input fd.
type kittyInput struct {
	reader     *vtinput.Reader
	events     <-chan *vtinput.InputEvent
	restore    func()
	pipeReader *io.PipeReader
	pipeWriter *io.PipeWriter
}

// enableKittyInput puts stdin in raw mode and pushes the kitty
// keyboard protocol. Mouse tracking is enabled separately by
// bubbletea (WithMouseCellMotion) for viewport wheel scrolling;
// bracketed paste is already handled by bubbletea's renderer.
func enableKittyInput() (*kittyInput, error) {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stdout.WriteString(kittyEnable); err != nil {
		_ = term.Restore(fd, oldState)
		return nil, err
	}
	// Give the terminal a moment to switch protocol state before the
	// reader starts consuming input.
	time.Sleep(50 * time.Millisecond)
	reader := vtinput.NewReader(os.Stdin, false)
	pr, pw := io.Pipe()
	return &kittyInput{
		reader: reader,
		events: reader.GetEventChan(),
		restore: func() {
			_, _ = os.Stdout.WriteString(kittyDisable)
			_ = term.Restore(fd, oldState)
		},
		pipeReader: pr,
		pipeWriter: pw,
	}, nil
}

// close restores the terminal and shuts the reader and bubbletea's
// input pipe down.
func (ki *kittyInput) close() {
	if ki.pipeWriter != nil {
		_ = ki.pipeWriter.Close() // unblocks bubbletea's read loop
	}
	if ki.restore != nil {
		ki.restore() // pops kitty protocol and restores raw mode
	}
	if ki.reader != nil {
		ki.reader.Close()
	}
}
