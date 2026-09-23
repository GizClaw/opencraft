// The keyboard map's contract: what a combo means, how it is shown, and
// which of the two gates (overlay, text field) let it through. These are
// the rules the dispatcher only wires up; keeping them here means a wrong
// gate fails a test instead of a keystroke.
import { describe, expect, it } from 'vitest';
import {
  formatCombo,
  formatCombos,
  isEditableTarget,
  matchesCombo,
  parseCombo,
  primaryCombo,
  resolveShortcut,
  SHORTCUTS,
  shortcutByID,
  type ShortcutEvent,
} from './keys';

/** event builds the subset of a KeyboardEvent the matcher reads. */
function event(init: Partial<ShortcutEvent> & { code: string }): ShortcutEvent {
  return {
    key: '',
    metaKey: false,
    ctrlKey: false,
    altKey: false,
    shiftKey: false,
    repeat: false,
    isComposing: false,
    keyCode: 0,
    ...init,
  };
}

const mac = { isMac: true, overlayOpen: false, editable: false };

describe('parseCombo', () => {
  it('reads modifiers and the physical key', () => {
    expect(parseCombo('Mod+Shift+[')).toMatchObject({
      mod: true,
      shift: true,
      alt: false,
      ctrl: false,
      code: 'BracketLeft',
    });
  });

  it('names letters, digits, punctuation and function keys', () => {
    expect(parseCombo('Mod+K')?.code).toBe('KeyK');
    expect(parseCombo('Mod+1')?.code).toBe('Digit1');
    expect(parseCombo('Mod+/')?.code).toBe('Slash');
    expect(parseCombo('Mod+ArrowUp')?.code).toBe('ArrowUp');
    expect(parseCombo('F5')?.code).toBe('F5');
  });

  it('rejects what it cannot name', () => {
    expect(parseCombo('Mod+Nope')).toBeUndefined();
    expect(parseCombo('Hyper+K')).toBeUndefined();
  });
});

describe('formatCombo', () => {
  it('uses glyphs on macOS and words elsewhere', () => {
    expect(formatCombo('Mod+K', true)).toBe('⌘K');
    expect(formatCombo('Mod+Shift+[', true)).toBe('⇧⌘[');
    expect(formatCombo('Mod+ArrowUp', true)).toBe('⌘↑');
    expect(formatCombo('Mod+K', false)).toBe('Ctrl+K');
    expect(formatCombo('Mod+Shift+[', false)).toBe('Ctrl+Shift+[');
  });

  it('keeps the fixed modifier order on both platforms', () => {
    expect(formatCombos(['Escape', 'Mod+.'], true)).toBe('Esc / ⌘.');
    expect(formatCombos(['Home', 'End'], false)).toBe('Home / End');
  });

  it('shows the primary combo for a mapped command', () => {
    expect(primaryCombo('turn.stop')).toBe('Escape');
    expect(formatCombo(primaryCombo('session.next'), true)).toBe('⌘]');
    // A command that is not in the table must simply have no badge.
    expect(primaryCombo('nope')).toBe('');
  });
});

describe('matchesCombo', () => {
  it('requires an exact modifier set', () => {
    const combo = 'Mod+Shift+T';
    expect(
      matchesCombo(
        event({ code: 'KeyT', metaKey: true, shiftKey: true }),
        combo,
        true,
      ),
    ).toBe(true);
    expect(
      matchesCombo(event({ code: 'KeyT', metaKey: true }), combo, true),
    ).toBe(false);
    expect(
      matchesCombo(
        event({ code: 'KeyT', metaKey: true, shiftKey: true, altKey: true }),
        combo,
        true,
      ),
    ).toBe(false);
  });

  it('matches shifted punctuation by physical key', () => {
    // Shift+[ reports '{' as the character on a US layout; the code is the
    // only stable reading.
    expect(
      matchesCombo(
        event({ code: 'BracketLeft', metaKey: true, shiftKey: true }),
        'Mod+Shift+[',
        true,
      ),
    ).toBe(true);
  });

  it('keeps Control out of the way on macOS', () => {
    // ⌃K/⌃N are text-field caret motions on macOS: only ⌘ may fire a Mod
    // combo there.
    expect(
      matchesCombo(event({ code: 'KeyK', ctrlKey: true }), 'Mod+K', true),
    ).toBe(false);
    expect(
      matchesCombo(event({ code: 'KeyK', ctrlKey: true }), 'Mod+K', false),
    ).toBe(true);
    expect(
      matchesCombo(event({ code: 'KeyK', metaKey: true }), 'Mod+K', false),
    ).toBe(false);
  });
});

describe('resolveShortcut', () => {
  it('finds the shortcut a combo belongs to', () => {
    const spec = resolveShortcut(
      SHORTCUTS,
      event({ code: 'KeyK', metaKey: true }),
      mac,
    );
    expect(spec?.id).toBe('palette.open');
  });

  it('never fires mid-composition', () => {
    const composing = event({ code: 'Escape', isComposing: true });
    expect(resolveShortcut(SHORTCUTS, composing, mac)).toBeUndefined();
    const legacy = event({ code: 'Enter', keyCode: 229 });
    expect(resolveShortcut(SHORTCUTS, legacy, mac)).toBeUndefined();
  });

  it('stands down for an open overlay unless it runs there', () => {
    const overlay = { ...mac, overlayOpen: true };
    // Acting on the surface under a dialog is what puts focus in two
    // places at once: the chat rail waits for the dialog to close.
    expect(
      resolveShortcut(
        SHORTCUTS,
        event({ code: 'KeyO', metaKey: true }),
        overlay,
      ),
    ).toBeUndefined();
    expect(
      resolveShortcut(
        SHORTCUTS,
        event({ code: 'ArrowRight', metaKey: true }),
        overlay,
      ),
    ).toBeUndefined();
    // Opening another surface, or a command no overlay owns, still runs.
    expect(
      resolveShortcut(
        SHORTCUTS,
        event({ code: 'KeyN', metaKey: true }),
        overlay,
      )?.id,
    ).toBe('chat.new');
    expect(
      resolveShortcut(
        SHORTCUTS,
        event({ code: 'Slash', metaKey: true }),
        overlay,
      )?.id,
    ).toBe('shortcuts.open');
  });

  it('stands down in a text field unless the shortcut runs there', () => {
    const editable = { ...mac, editable: true };
    expect(
      resolveShortcut(
        SHORTCUTS,
        event({ code: 'ArrowUp', metaKey: true }),
        editable,
      ),
    ).toBeUndefined();
    expect(
      resolveShortcut(
        SHORTCUTS,
        event({ code: 'KeyN', metaKey: true }),
        editable,
      )?.id,
    ).toBe('chat.new');
  });

  it('ignores auto-repeat except where a row asks for it', () => {
    const held = event({ code: 'KeyN', metaKey: true, repeat: true });
    expect(resolveShortcut(SHORTCUTS, held, mac)).toBeUndefined();
    const holding = event({ code: 'ArrowDown', metaKey: true, repeat: true });
    expect(resolveShortcut(SHORTCUTS, holding, mac)?.id).toBe('chat.nextUser');
  });

  it('keeps transcript walks out of text fields', () => {
    // ⌘↑ walks a textarea to its start on macOS, so the row may not claim
    // the key while the caret is in a field.
    expect(shortcutByID('chat.prevUser')?.editable).toBeUndefined();
    expect(
      resolveShortcut(SHORTCUTS, event({ code: 'ArrowUp', metaKey: true }), mac)
        ?.id,
    ).toBe('chat.prevUser');
    expect(
      resolveShortcut(SHORTCUTS, event({ code: 'ArrowUp', metaKey: true }), {
        ...mac,
        editable: true,
      }),
    ).toBeUndefined();
  });

  it('keeps a combo a text surface owns even when the row runs in fields', () => {
    // turn.stop is editable only through ⌘.: Escape belongs to the field
    // (the sidebar's rename box cancels, a path field closes), so the
    // shell may not take it there — the composer runs the command from
    // its own key handler instead.
    const editable = { ...mac, editable: true };
    expect(shortcutByID('turn.stop')?.editable).toEqual(['Mod+.']);
    expect(
      resolveShortcut(SHORTCUTS, event({ code: 'Escape' }), editable),
    ).toBeUndefined();
    expect(
      resolveShortcut(
        SHORTCUTS,
        event({ code: 'Period', metaKey: true }),
        editable,
      )?.id,
    ).toBe('turn.stop');
    // Outside a field both of the row's combos run.
    expect(resolveShortcut(SHORTCUTS, event({ code: 'Escape' }), mac)?.id).toBe(
      'turn.stop',
    );
    expect(
      resolveShortcut(SHORTCUTS, event({ code: 'Period', metaKey: true }), mac)
        ?.id,
    ).toBe('turn.stop');
  });
});

describe('isEditableTarget', () => {
  it('recognises the surfaces the caret can be in', () => {
    const input = document.createElement('input');
    const textarea = document.createElement('textarea');
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    const editable = document.createElement('div');
    editable.contentEditable = 'true';
    const plain = document.createElement('div');
    expect(isEditableTarget(input)).toBe(true);
    expect(isEditableTarget(textarea)).toBe(true);
    expect(isEditableTarget(editable)).toBe(true);
    expect(isEditableTarget(checkbox)).toBe(false);
    expect(isEditableTarget(plain)).toBe(false);
    expect(isEditableTarget(null)).toBe(false);
    expect(isEditableTarget(document)).toBe(false);
  });
});
