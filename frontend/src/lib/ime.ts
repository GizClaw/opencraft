import { useCallback, useMemo, useRef, useState } from 'react';
import type { CompositionEvent } from 'react';

// IME handling, in one place.
//
// An input method editor splits a key press from what the app sees in two
// ways, and every surface that acts on a key has to know about both:
//
//  1. While a composition is in flight the IME owns the keyboard. Enter
//     confirms a candidate, Escape drops the candidate window, ↑/↓ walk
//     it. The engine reports that as `isComposing` on the keydown, and as
//     the legacy `keyCode === 229` on the engines that never set the
//     flag. A handler must stand down for those events *without*
//     cancelling them: the browser needs them for the composition to
//     work at all.
//
//  2. Chromium — and therefore WebView2 on Windows — ends a composition
//     with a key the page then receives again as an ordinary keydown:
//     compositionend fires first and the keydown second, already with
//     `isComposing === false` (issues.chromium.org/418103018, still open;
//     WebKit and Gecko report the press *before* compositionend, with the
//     flag set). Enter and Escape are how a Chinese or Japanese IME
//     commits and cancels, so that stray delivery looks exactly like the
//     user pressing the key: the palette would run its highlighted
//     command, the composer would send, a dialog would close — all from a
//     press the candidate window has already spent.
//
// The first case is a question the callers ask (`imeKeyOwner`); the second
// is the same question plus a little state, because the stray keydown is
// separated from the compositionend that explains it. What keeps the two
// from colliding is the rule the events themselves obey: a press the page
// *did* see is reported before compositionend (case 1's ordering), and one
// it did not is reported after and within the same input batch.

/**
 * What a keydown belongs to:
 *
 *  - 'composition': the IME is composing. Do nothing, and do not cancel
 *    the event — the composer's candidate window needs Enter/Escape to
 *    keep working.
 *  - 'committed': the stray delivery of a press that finished a
 *    composition. It has already been cancelled at the source (the
 *    document-capture listener below), so a handler that still sees one
 *    through another route must not act on it either.
 *  - null: the app owns the key.
 */
export type IMEKeyOwner = 'composition' | 'committed';

/** The KeyboardEvent fields this module reads. */
export interface IMEKeyEvent {
  key: string;
  isComposing: boolean;
  keyCode: number;
  /** KeyboardEvent.timeStamp; the table-shaped test events may omit it. */
  timeStamp?: number;
}

// The keys whose stray delivery *runs* something: Enter submits, sends or
// runs the highlighted row; Escape closes the surface or stops the turn.
// Space is deliberately not here: it commits a candidate in CJK IMEs too,
// but a stray space would at worst type one space, while swallowing a
// space the user did type right after committing would be the worse bug.
const GUARDED_KEYS: readonly string[] = ['Enter', 'Escape'];

// How long a stray delivery may trail its compositionend. The pair comes
// from one press, so in practice it lands in the same input batch (~ms);
// the window only has to be generous enough for an engine that spreads
// them over a couple of tasks, because its other job is to expire the arm
// in the case where the composition ended with *no* key at all (a mouse
// click on a candidate, a blur) — there the user's next Enter or Escape
// must not be eaten.
const STRAY_WINDOW_MS = 150;

// A keydown this close to compositionend is the press that ended the
// composition, reported in the order WebKit and Gecko use. Nothing more is
// coming for that press, so nothing is armed. Without this gate the second
// Enter of the commit-then-send double press — the normal way to send a
// just-committed Chinese sentence on macOS — would be swallowed.
const SAME_PRESS_MS = 30;

// Armed by a compositionend whose press the page has not seen yet; the
// stamp of that event, or -1 when disarmed.
let strayArmedAt = -1;
// The stamp of the last keydown, whatever it was, so compositionend can
// tell whether the press ending the composition was already reported.
let lastKeydownAt = -Infinity;

// What has already been decided about an event object. A single keydown
// passes the shell's window listener, this module's document listener and
// the focused surface's own handler; the answer — including "this one has
// been dealt with" — must not change between them.
const owners = new WeakMap<object, IMEKeyOwner>();

function stampOf(event: { timeStamp?: number }): number {
  return typeof event.timeStamp === 'number' && event.timeStamp > 0
    ? event.timeStamp
    : Date.now();
}

function classify(event: IMEKeyEvent): IMEKeyOwner | null {
  const remembered = owners.get(event);
  if (remembered !== undefined) return remembered;
  if (event.isComposing || event.keyCode === 229) return 'composition';
  if (strayArmedAt < 0) return null;
  const stray =
    GUARDED_KEYS.includes(event.key) &&
    stampOf(event) - strayArmedAt <= STRAY_WINDOW_MS;
  // Either this keydown was the stray delivery or it was a real press:
  // both end the arm, so it can never swallow a second key.
  strayArmedAt = -1;
  if (!stray) return null;
  owners.set(event, 'committed');
  return 'committed';
}

/**
 * imeKeyOwner answers who owns a keydown — the read every key handler
 * needs before it acts (see IMEKeyOwner for the three answers).
 */
export function imeKeyOwner(event: IMEKeyEvent): IMEKeyOwner | null {
  return classify(event);
}

let listening = false;

// The document listens once, at load: composition events bubble to it from
// whatever surface has the IME, and the capture phase sees a keydown before
// any surface handler can act on it.
function listen(): void {
  if (listening || typeof document === 'undefined') return;
  listening = true;
  document.addEventListener(
    'compositionstart',
    () => {
      // A new composition means the previous press is over. (Chromium
      // delivers the first key of a new composition after its
      // compositionstart; the letter belongs to the app's text, not to a
      // stray guard.)
      strayArmedAt = -1;
    },
    true,
  );
  document.addEventListener(
    'compositionend',
    (event) => {
      const end = stampOf(event);
      strayArmedAt = end - lastKeydownAt >= SAME_PRESS_MS ? end : -1;
    },
    true,
  );
  document.addEventListener(
    'keydown',
    (event) => {
      lastKeydownAt = stampOf(event);
      if (classify(event) !== 'committed') return;
      // The IME already took this press. Cancel it here so nothing
      // downstream acts on it: not the focused surface, not a default
      // button, not a form.
      event.preventDefault();
      event.stopPropagation();
    },
    true,
  );
}

listen();

/** resetIMEState drops the module's memory of a press in flight. Tests
 * that hand-build composition sequences use it between cases; the app has
 * no reason to (every sequence it sees is ended by the engine). */
export function resetIMEState(): void {
  strayArmedAt = -1;
  lastKeydownAt = -Infinity;
}

export interface CompositionBinding {
  onCompositionStart: () => void;
  onCompositionEnd: (event: CompositionEvent<HTMLInputElement>) => void;
}

/**
 * useComposition tracks whether an input has a composition in flight, and
 * hands out the two handlers that keep the state honest. A surface whose
 * list is derived from what the user typed wants this: filtering the
 * pinyin letters empties the list under the candidate window and re-fills
 * it when the characters land, which is a flicker nobody asked for — so
 * the frozen list is what the component renders while `composing` is true.
 * `onCommit` receives the value the composition ended on, for the surfaces
 * that keep their own copy of the field's text.
 */
export function useComposition(onCommit?: (value: string) => void): {
  composing: boolean;
  bind: CompositionBinding;
  /** Drop a composition the input will never finish (a menu closed over
   * it): the end event is not coming, so the flag would stay stuck. */
  reset: () => void;
} {
  const [composing, setComposing] = useState(false);
  const commitRef = useRef(onCommit);
  commitRef.current = onCommit;
  const bind = useMemo<CompositionBinding>(
    () => ({
      onCompositionStart: () => setComposing(true),
      onCompositionEnd: (event) => {
        setComposing(false);
        commitRef.current?.(event.currentTarget.value);
      },
    }),
    [],
  );
  const reset = useCallback(() => setComposing(false), []);
  return { composing, bind, reset };
}
