package tui

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/bubbletea"
	"github.com/unxed/vtinput"
)

func TestKittyEnableFlags(t *testing.T) {
	// Flag 8 ("report all keys as escape codes") must stay off:
	// terminals then encode IME-committed text as CSI-u events, which
	// breaks Chinese/Japanese input on several terminals. codex-rs
	// pushes the same 1|2|4 set.
	if kittyEnable != "\x1b[>7u" {
		t.Fatalf("kittyEnable = %q, want \\x1b[>7u (flags 1|2|4)", kittyEnable)
	}
}

// readKey feeds raw terminal bytes through vtinput (the same path the
// app uses) and maps the resulting event to a KeyMsg.
func readKey(t *testing.T, data []byte) (tea.KeyMsg, bool) {
	t.Helper()
	r := vtinput.NewReader(bytes.NewReader(data), false)
	defer r.Close()
	ev, err := r.ReadEvent()
	if err != nil {
		t.Fatalf("ReadEvent(%q): %v", data, err)
	}
	return mapInputEvent(ev)
}

func TestMapInputEvent(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string // msg.String(); empty means the event is dropped
	}{
		// Shift+Enter / Option+Enter insert a newline (codex-rs binds
		// both for insert_newline).
		{"shift enter", []byte("\x1b[13;2u"), "ctrl+j"},
		{"shift enter event typed", []byte("\x1b[13;2:1u"), "ctrl+j"},
		{"option enter", []byte("\x1b[13;3u"), "ctrl+j"},
		{"shift option enter", []byte("\x1b[13;4u"), "ctrl+j"},
		// Plain Enter submits.
		{"plain enter", []byte("\r"), "enter"},
		{"plain enter kitty", []byte("\x1b[13;1u"), "enter"},
		{"plain enter bare", []byte("\x1b[13u"), "enter"},
		// Ctrl+Enter is dropped.
		{"ctrl enter dropped", []byte("\x1b[13;5u"), ""},
		{"ctrl shift enter dropped", []byte("\x1b[13;6u"), ""},
		{"ctrl option enter dropped", []byte("\x1b[13;7u"), ""},
		// Esc.
		{"esc", []byte("\x1b"), "esc"},
		{"esc kitty", []byte("\x1b[27;1u"), "esc"},
		{"esc event typed", []byte("\x1b[27;1:1u"), "esc"},
		{"ctrl esc dropped", []byte("\x1b[27;5u"), ""},
		// Ctrl+letter keeps working under the kitty protocol (this was
		// the bug: CSI-u events for Ctrl+letter used to be dropped).
		{"ctrl+c legacy", []byte{0x03}, "ctrl+c"},
		{"ctrl+c kitty", []byte("\x1b[99;5u"), "ctrl+c"},
		{"ctrl+t kitty", []byte("\x1b[116;5u"), "ctrl+t"},
		{"ctrl+z kitty", []byte("\x1b[122;5u"), "ctrl+z"},
		// Navigation keys.
		{"up", []byte("\x1b[A"), "up"},
		{"down", []byte("\x1b[B"), "down"},
		{"right", []byte("\x1b[C"), "right"},
		{"left", []byte("\x1b[D"), "left"},
		{"pgup", []byte("\x1b[5~"), "pgup"},
		{"pgdown", []byte("\x1b[6~"), "pgdown"},
		{"home", []byte("\x1b[H"), "home"},
		{"end", []byte("\x1b[F"), "end"},
		{"backspace", []byte{0x7f}, "backspace"},
		{"delete", []byte("\x1b[3~"), "delete"},
		{"tab", []byte("\t"), "tab"},
		{"shift tab", []byte("\x1b[Z"), "shift+tab"},
		// Plain characters.
		{"letter", []byte("a"), "a"},
		{"letter kitty", []byte("\x1b[97u"), "a"},
		{"shift letter kitty", []byte("\x1b[97;2u"), "A"},
		{"space", []byte(" "), " "},
		{"cjk kitty", []byte("\x1b[20320u"), "你"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, ok := readKey(t, tc.in)
			if tc.want == "" {
				if ok {
					t.Fatalf("key %q = %q, want dropped", tc.in, msg)
				}
				return
			}
			if !ok {
				t.Fatalf("key %q dropped, want %q", tc.in, tc.want)
			}
			if got := msg.String(); got != tc.want {
				t.Fatalf("key %q = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMapInputEventDropsNonKeyEvents(t *testing.T) {
	// Paste markers and focus events are not key presses.
	for _, ev := range []*vtinput.InputEvent{
		{Type: vtinput.PasteEventType, PasteStart: true},
		{Type: vtinput.PasteEventType, PasteStart: false},
		{Type: vtinput.FocusEventType, SetFocus: true},
		{Type: vtinput.MouseEventType},
		{Type: vtinput.KeyEventType, KeyDown: false, VirtualKeyCode: vtinput.VK_C,
			ControlKeyState: vtinput.LeftCtrlPressed}, // keyup
	} {
		if msg, ok := mapInputEvent(ev); ok {
			t.Fatalf("event %+v mapped to %q, want dropped", ev, msg)
		}
	}
}

func TestMapInputEventAltBindings(t *testing.T) {
	// Alt+letter is used by the textarea for word navigation (alt+f,
	// alt+b). vtinput uppercases Alt-modified chars; the adapter must
	// restore the lowercase form so key bindings match.
	msg, ok := mapInputEvent(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_F,
		Char:            'F',
		ControlKeyState: vtinput.LeftAltPressed,
	})
	if !ok {
		t.Fatal("alt+f dropped")
	}
	if got := msg.String(); got != "alt+f" {
		t.Fatalf("alt+f = %q, want alt+f", got)
	}

	// Alt+arrow (no dedicated KeyType; carried on the Alt flag).
	msg, ok = mapInputEvent(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_LEFT,
		ControlKeyState: vtinput.LeftAltPressed,
	})
	if !ok {
		t.Fatal("alt+left dropped")
	}
	if got := msg.String(); got != "alt+left" {
		t.Fatalf("alt+left = %q, want alt+left", got)
	}
}
