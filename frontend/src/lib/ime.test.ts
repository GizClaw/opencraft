// The IME contract (see the comment at the top of ime.ts): mid-composition
// keys belong to the candidate window, and the one keydown Chromium delivers
// *after* a composition ends is the same press — it must reach nothing.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, renderHook } from '@testing-library/react';
import { imeKeyOwner, resetIMEState, useComposition } from './ime';

/** field is a real input in the document: the module listens on the
 * document, so an event dispatched into a detached node is not its. */
function field(): HTMLInputElement {
  const input = document.createElement('input');
  document.body.append(input);
  return input;
}

/** keydown dispatches a real keydown, with the legacy fields jsdom's
 * constructor does not take (isComposing, keyCode). */
function keydown(
  target: EventTarget,
  init: KeyboardEventInit & { isComposing?: boolean; keyCode?: number },
): KeyboardEvent {
  const { isComposing, keyCode, ...rest } = init;
  const event = new KeyboardEvent('keydown', {
    bubbles: true,
    cancelable: true,
    ...rest,
  });
  if (isComposing !== undefined) {
    Object.defineProperty(event, 'isComposing', { value: isComposing });
  }
  if (keyCode !== undefined) {
    Object.defineProperty(event, 'keyCode', { value: keyCode });
  }
  target.dispatchEvent(event);
  return event;
}

beforeEach(() => {
  resetIMEState();
  document.body.replaceChildren();
});

describe('imeKeyOwner', () => {
  it('hands a keydown during a composition to the IME, and leaves it alone', () => {
    const input = field();
    const composing = keydown(input, {
      key: 'Enter',
      isComposing: true,
      keyCode: 229,
    });
    expect(imeKeyOwner(composing)).toBe('composition');
    // Nothing about it is cancelled: the candidate window needs the event.
    expect(composing.defaultPrevented).toBe(false);
  });

  it('reads the legacy 229 as a composition too', () => {
    const input = field();
    const composing = keydown(input, { key: 'Escape', keyCode: 229 });
    expect(imeKeyOwner(composing)).toBe('composition');
  });

  it('claims the stray keydown Chromium delivers after compositionend', () => {
    const input = field();
    const seen = vi.fn();
    input.addEventListener('keydown', seen);
    fireEvent.compositionEnd(input);

    // Chromium's order: compositionend first, the press's own keydown
    // second, already with isComposing false.
    const stray = keydown(input, { key: 'Enter', keyCode: 13 });
    expect(imeKeyOwner(stray)).toBe('committed');
    // Cancelled at the source, so nothing downstream sees it.
    expect(stray.defaultPrevented).toBe(true);
    expect(seen).not.toHaveBeenCalled();
  });

  it('claims a stray Escape the same way', () => {
    const input = field();
    fireEvent.compositionEnd(input);
    const stray = keydown(input, { key: 'Escape' });
    expect(stray.defaultPrevented).toBe(true);
  });

  it('leaves the second Enter of a commit-then-send press alone', () => {
    const input = field();
    fireEvent.compositionEnd(input);
    keydown(input, { key: 'Enter' }); // the press itself
    const second = keydown(input, { key: 'Enter' });
    expect(second.defaultPrevented).toBe(false);
    expect(imeKeyOwner(second)).toBeNull();
  });

  it('arms nothing when the press was reported before compositionend', () => {
    // WebKit, Gecko and the spec: the committing keydown arrives first,
    // with the flag set. Nothing is left to swallow, so the next Enter —
    // the one the user pressed to send — has to go through.
    const input = field();
    keydown(input, { key: 'Enter', isComposing: true, keyCode: 229 });
    fireEvent.compositionEnd(input);
    const second = keydown(input, { key: 'Enter' });
    expect(second.defaultPrevented).toBe(false);
  });

  it('ignores a keydown the app owned when judging the same press', () => {
    // An ordinary keydown says nothing about the composition, so it must
    // not answer the question compositionend asks. Taking the last
    // keydown of any kind as "this press was already reported" disarms
    // the guard — and the stray Enter runs the highlighted command.
    const input = field();
    keydown(input, { key: 'Enter' }); // the app's own press, moments before
    fireEvent.compositionEnd(input);
    const stray = keydown(input, { key: 'Enter' });
    expect(imeKeyOwner(stray)).toBe('committed');
    expect(stray.defaultPrevented).toBe(true);
  });

  it('expires: a commit with no key at all does not eat the next Enter', () => {
    vi.useFakeTimers();
    try {
      const input = field();
      fireEvent.compositionEnd(input); // e.g. a mouse click on a candidate
      act(() => {
        vi.advanceTimersByTime(1000);
      });
      const late = keydown(input, { key: 'Enter' });
      expect(late.defaultPrevented).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it('disarms on the next ordinary key', () => {
    const input = field();
    fireEvent.compositionEnd(input);
    keydown(input, { key: 'a' });
    const enter = keydown(input, { key: 'Enter' });
    expect(enter.defaultPrevented).toBe(false);
  });

  it('does not guard Space, which an IME also commits with', () => {
    // Deliberate: swallowing a space the user typed right after
    // committing would be the worse bug (see GUARDED_KEYS).
    const input = field();
    fireEvent.compositionEnd(input);
    const space = keydown(input, { key: ' ' });
    expect(space.defaultPrevented).toBe(false);
  });

  it('remembers what it decided about one event', () => {
    // The same keydown passes several listeners; the answer has to be the
    // same for all of them, or a later one would act on a press the first
    // one already dealt with.
    const input = field();
    fireEvent.compositionEnd(input);
    const stray = keydown(input, { key: 'Enter' });
    expect(imeKeyOwner(stray)).toBe('committed');
    expect(imeKeyOwner(stray)).toBe('committed');
  });
});

describe('useComposition', () => {
  it('tracks a composition and reports the value it ended on', () => {
    const committed: string[] = [];
    const { result } = renderHook(() =>
      useComposition((value) => committed.push(value)),
    );
    expect(result.current.composing).toBe(false);

    act(() => result.current.bind.onCompositionStart());
    expect(result.current.composing).toBe(true);

    const input = field();
    input.value = '你好';
    act(() =>
      result.current.bind.onCompositionEnd({
        currentTarget: input,
      } as never),
    );
    expect(result.current.composing).toBe(false);
    expect(committed).toEqual(['你好']);
  });

  it('forgets a composition the input will never finish', () => {
    const { result } = renderHook(() => useComposition());
    act(() => result.current.bind.onCompositionStart());
    act(() => result.current.reset());
    expect(result.current.composing).toBe(false);
  });
});
