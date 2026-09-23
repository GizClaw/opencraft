import { useSyncExternalStore } from 'react';

// The hints the shell draws *because* a modifier is down: the session
// slots number the sidebar rows while ⌘ (Control elsewhere) is held and
// go away when it comes up. That is the convention the menu bars set —
// their accelerators reveal themselves while the modifier is held — and
// it is what lets a hint live on a row without taking a strip of every
// title for good.
//
// It is deliberately not part of lib/keys.ts: pressing ⌘ is not a
// shortcut, it is the thing that turns shortcut *hints* on, and it has to
// be answered for presses no combo matches. A down/up pair is not enough
// either — the up never arrives when the press ends somewhere else
// (⌘-Tab, Spotlight, Mission Control, the window losing focus), which
// would leave the hints stuck on. So the flags are recomputed from every
// keyboard event's own modifier state — the browser fills those from the
// physical keys at the moment the event was created, which is the truth
// this wants — and cleared on the two events that say the keyboard is not
// ours any more.
//
// Both modifiers are tracked and the caller picks the one its platform
// binds (App's isMac), so this module needs no user-agent sniffing of its
// own and one listener serves every surface that asks. A surface using
// the answer keeps its hint in the layout and toggles only visibility:
// revealing the hints must not reflow the row the reader is looking at.

interface ModifierState {
  meta: boolean;
  control: boolean;
}

const RELEASED: ModifierState = { meta: false, control: false };

let state: ModifierState = RELEASED;
let listening = false;
const listeners = new Set<() => void>();

function notify() {
  for (const listener of listeners) listener();
}

/**
 * setState publishes a snapshot only when a flag actually moved. Without
 * that, every keystroke of a sentence would wake each subscriber.
 */
function setState(next: ModifierState) {
  if (next.meta === state.meta && next.control === state.control) return;
  state = next;
  notify();
}

function onKey(event: KeyboardEvent) {
  setState({ meta: event.metaKey, control: event.ctrlKey });
}

/**
 * onRelease clears both flags: blur and a hidden page are the two events
 * that say the keyboard left mid-press, with the keyup going to whatever
 * took focus, or to nobody at all.
 */
function onRelease() {
  setState(RELEASED);
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (!listening) {
    // Capture, like the shell's other window listeners: a surface that
    // stops propagation on a key still leaves the modifier state true.
    window.addEventListener('keydown', onKey, true);
    window.addEventListener('keyup', onKey, true);
    window.addEventListener('blur', onRelease);
    document.addEventListener('visibilitychange', onRelease);
    listening = true;
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size > 0 || !listening) return;
    window.removeEventListener('keydown', onKey, true);
    window.removeEventListener('keyup', onKey, true);
    window.removeEventListener('blur', onRelease);
    document.removeEventListener('visibilitychange', onRelease);
    listening = false;
    // The next subscriber starts from "not held": whatever was true when
    // the last one left may have ended with nobody listening.
    state = RELEASED;
  };
}

/**
 * useModifierHeld reports whether the modifier this platform binds is
 * currently down — ⌘ on macOS, Control elsewhere, the same split the
 * combos use (lib/keys.ts).
 */
export function useModifierHeld(isMac: boolean): boolean {
  return useSyncExternalStore(subscribe, () =>
    isMac ? state.meta : state.control,
  );
}
