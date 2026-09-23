// The keyboard map: one table, read by the dispatcher, the command
// palette's key column and the shortcut sheet. Before this file the
// window-level keys were three separate listeners in App.tsx plus a
// hand-rolled modifier check per surface, so a key could be documented
// in one place and bound in another, and nothing could answer "what is
// bound right now?" — which is exactly what a shortcuts overlay and a
// palette badge need.
//
// Only keys the *shell* owns live here. Keys a surface already handles
// while it has focus (Enter in the composer, ↑↓ in a list) are documented
// in KEY_REFERENCES instead: they belong to that surface, and listing
// them as commands would suggest the shell can run them from anywhere.

import { imeKeyOwner, type IMEKeyEvent } from './ime';

/**
 * Sections of the shortcut sheet. A command row and a reference row can
 * share a section; the section is what the reader is doing, not who
 * handles the key.
 */
export type ShortcutGroup =
  'global' | 'chat' | 'composer' | 'lists' | 'palette';

export interface ShortcutSpec {
  /**
   * Stable id. It is also the command id the native macOS menu bridges
   * back (see internal/adapters/desktop/menuspec.go), so the two tables
   * name the same actions.
   */
  id: string;
  group: ShortcutGroup;
  /** i18n key of what the key does. */
  label: string;
  /** Canonical combos; the first one is the one the UI shows. */
  combos: string[];
  /**
   * Fire while a text field owns the caret: `true` for every combo,
   * `false` (the default) for none, or the list of combos that may.
   *
   * The default is no: a combo without a modifier would otherwise shadow
   * typing, and the ones with a modifier often mean something inside a
   * field (⌘↑ walks a textarea to its start). The list form is for a
   * command whose combos differ in exactly that way — see Escape in
   * `turn.stop`.
   */
  editable?: boolean | string[];
  /**
   * Fire while an overlay owns the keyboard. Default 'blocked': a dialog
   * or menu is the topmost surface, and a command that acts on the
   * surface underneath it either does nothing visible or fights the
   * dialog for focus. 'runs' is for the commands that are about the
   * overlays themselves (opening one; navigating to another surface,
   * which closes the overlay on its way) or that change nothing an
   * overlay owns (a copy, the theme).
   */
  overlay?: 'blocked' | 'runs';
  /** Fire repeatedly while the key is held. Default false. */
  repeat?: boolean;
}

/** A key a surface owns; documentation only, never dispatched. */
export interface KeyReference {
  group: ShortcutGroup;
  label: string;
  keys: string[];
}

/** group → i18n key of its heading. */
export const GROUP_LABELS: Record<ShortcutGroup, string> = {
  global: 'shortcuts.groupGlobal',
  chat: 'shortcuts.groupChat',
  composer: 'shortcuts.groupComposer',
  lists: 'shortcuts.groupLists',
  palette: 'shortcuts.groupPalette',
};

export const SHORTCUTS: ShortcutSpec[] = [
  {
    id: 'palette.open',
    group: 'global',
    label: 'shortcuts.openPalette',
    combos: ['Mod+K'],
    editable: true,
    overlay: 'runs',
  },
  {
    id: 'shortcuts.open',
    group: 'global',
    label: 'shortcuts.openSheet',
    combos: ['Mod+/'],
    editable: true,
    overlay: 'runs',
  },
  {
    id: 'chat.new',
    group: 'global',
    label: 'shortcuts.newChat',
    combos: ['Mod+N'],
    editable: true,
    overlay: 'runs',
  },
  {
    id: 'settings.open',
    group: 'global',
    label: 'shortcuts.openSettings',
    combos: ['Mod+,'],
    editable: true,
    overlay: 'runs',
  },
  {
    id: 'panel.files',
    group: 'global',
    label: 'shortcuts.filesPanel',
    combos: ['Mod+O'],
    editable: true,
  },
  {
    id: 'panel.git',
    group: 'global',
    label: 'shortcuts.gitPanel',
    combos: ['Mod+Shift+G'],
    editable: true,
  },
  {
    id: 'theme.cycle',
    group: 'global',
    label: 'shortcuts.cycleTheme',
    combos: ['Mod+Shift+T'],
    editable: true,
    overlay: 'runs',
  },
  {
    id: 'turn.stop',
    group: 'global',
    label: 'shortcuts.stopTurn',
    // Escape reaches the shell only when no overlay is open (the overlay
    // stack owns it, and the row stands down while a layer is registered),
    // which is why the sheet documents both ways to stop a turn.
    combos: ['Escape', 'Mod+.'],
    // ⌘. fires anywhere — it is the menu bar's spelling of this command
    // and means nothing inside a field. Escape does not: a text surface
    // claims it first (the sidebar's rename field cancels, a path field
    // closes, the composer's mention popup exits), so the shell may only
    // take it where no surface owns it. The composer is the one surface
    // that does want it (that is where the user is when they decide to
    // stop the reply), and it runs this same command from its own key
    // handler — see MarkdownComposer's onStop.
    editable: ['Mod+.'],
  },
  {
    id: 'session.prev',
    group: 'global',
    label: 'shortcuts.prevSession',
    combos: ['Mod+['],
    editable: true,
  },
  {
    id: 'session.next',
    group: 'global',
    label: 'shortcuts.nextSession',
    combos: ['Mod+]'],
    editable: true,
  },
  // Mod+1 … Mod+4 are the active workspace's session list, row by row, in
  // the order the sidebar draws it — running conversations first, then the
  // stored rows. lib/sessionSlots.ts owns that order; the sidebar numbers
  // its rows from the same function, so the digit and the number on screen
  // cannot drift apart. A slot past the end of the list does nothing.
  {
    id: 'session.slot1',
    group: 'global',
    label: 'shortcuts.session1',
    combos: ['Mod+1'],
    editable: true,
  },
  {
    id: 'session.slot2',
    group: 'global',
    label: 'shortcuts.session2',
    combos: ['Mod+2'],
    editable: true,
  },
  {
    id: 'session.slot3',
    group: 'global',
    label: 'shortcuts.session3',
    combos: ['Mod+3'],
    editable: true,
  },
  {
    id: 'session.slot4',
    group: 'global',
    label: 'shortcuts.session4',
    combos: ['Mod+4'],
    editable: true,
  },
  {
    id: 'workspace.prev',
    group: 'global',
    label: 'shortcuts.prevWorkspace',
    combos: ['Mod+Shift+['],
    editable: true,
  },
  {
    id: 'workspace.next',
    group: 'global',
    label: 'shortcuts.nextWorkspace',
    combos: ['Mod+Shift+]'],
    editable: true,
  },
  {
    id: 'chat.focusComposer',
    group: 'chat',
    label: 'shortcuts.focusComposer',
    combos: ['Mod+L'],
    editable: true,
  },
  {
    id: 'chat.copyLastReply',
    group: 'chat',
    label: 'shortcuts.copyLastReply',
    combos: ['Mod+Shift+C'],
    editable: true,
    overlay: 'runs',
  },
  {
    id: 'chat.prevUser',
    group: 'chat',
    label: 'shortcuts.prevUser',
    combos: ['Mod+ArrowUp'],
    // Not editable: ⌘↑ inside a text field is "jump to the start of the
    // field" on macOS. Held, it walks the conversation a turn at a time.
    repeat: true,
  },
  {
    id: 'chat.nextUser',
    group: 'chat',
    label: 'shortcuts.nextUser',
    combos: ['Mod+ArrowDown'],
    repeat: true,
  },
];

export const KEY_REFERENCES: KeyReference[] = [
  { group: 'composer', label: 'shortcuts.refSend', keys: ['Enter'] },
  { group: 'composer', label: 'shortcuts.refNewline', keys: ['Shift+Enter'] },
  { group: 'composer', label: 'shortcuts.refQueue', keys: ['Tab'] },
  { group: 'composer', label: 'shortcuts.refInterrupt', keys: ['Mod+Enter'] },
  // Escape from the composer runs the shell's stop command (see
  // turn.stop), so it is listed in the composer group as well as in
  // the global one.
  { group: 'composer', label: 'shortcuts.refStop', keys: ['Escape'] },
  { group: 'chat', label: 'shortcuts.refPromptPick', keys: ['Space'] },
  { group: 'chat', label: 'shortcuts.refPromptSubmit', keys: ['Enter'] },
  {
    group: 'lists',
    label: 'shortcuts.refRulerWalk',
    keys: ['ArrowUp', 'ArrowDown'],
  },
  { group: 'lists', label: 'shortcuts.refRulerEnds', keys: ['Home', 'End'] },
  { group: 'lists', label: 'shortcuts.refRulerJump', keys: ['Enter'] },
  {
    group: 'lists',
    label: 'shortcuts.refGitWalk',
    keys: ['ArrowUp', 'ArrowDown'],
  },
  {
    group: 'lists',
    label: 'shortcuts.refSeamResize',
    keys: ['ArrowLeft', 'ArrowRight'],
  },
  {
    group: 'lists',
    label: 'shortcuts.refSeamFine',
    keys: ['Shift+ArrowLeft', 'Shift+ArrowRight'],
  },
  {
    group: 'palette',
    label: 'shortcuts.refPaletteWalk',
    keys: ['ArrowUp', 'ArrowDown'],
  },
  { group: 'palette', label: 'shortcuts.refPaletteRun', keys: ['Enter'] },
  { group: 'palette', label: 'shortcuts.refPaletteClose', keys: ['Escape'] },
];

const byID = new Map(SHORTCUTS.map((spec) => [spec.id, spec]));

export function shortcutByID(id: string): ShortcutSpec | undefined {
  return byID.get(id);
}

/** The combo the UI shows for a shortcut ('' when it is not in the table). */
export function primaryCombo(id: string): string {
  return byID.get(id)?.combos[0] ?? '';
}

export interface ParsedCombo {
  mod: boolean;
  ctrl: boolean;
  alt: boolean;
  shift: boolean;
  /** KeyboardEvent.code — see comboCode. */
  code: string;
  /** The key half of the label, before platform glyphs are applied. */
  display: string;
}

// Named keys carry their own KeyboardEvent.code and label.
const NAMED_KEYS: Record<string, { code: string; display: string }> = {
  ArrowUp: { code: 'ArrowUp', display: '↑' },
  ArrowDown: { code: 'ArrowDown', display: '↓' },
  ArrowLeft: { code: 'ArrowLeft', display: '←' },
  ArrowRight: { code: 'ArrowRight', display: '→' },
  Home: { code: 'Home', display: 'Home' },
  End: { code: 'End', display: 'End' },
  Enter: { code: 'Enter', display: 'Enter' },
  Tab: { code: 'Tab', display: 'Tab' },
  Space: { code: 'Space', display: 'Space' },
  Escape: { code: 'Escape', display: 'Esc' },
  Backspace: { code: 'Backspace', display: '⌫' },
};

// Punctuation is written the way the user types it and matched by physical
// key: with Shift held, `event.key` reports the shifted character ("{" for
// Shift+[), so a character-based match would miss every ⇧ combination.
const PUNCTUATION: Record<string, { code: string; display: string }> = {
  '[': { code: 'BracketLeft', display: '[' },
  ']': { code: 'BracketRight', display: ']' },
  '/': { code: 'Slash', display: '/' },
  ',': { code: 'Comma', display: ',' },
  '.': { code: 'Period', display: '.' },
  '-': { code: 'Minus', display: '-' },
  '=': { code: 'Equal', display: '=' },
  ';': { code: 'Semicolon', display: ';' },
  "'": { code: 'Quote', display: "'" },
};

/** keyName looks a key up in both tables, letters last. */
function keyInfo(name: string): { code: string; display: string } | undefined {
  const named = NAMED_KEYS[name] ?? PUNCTUATION[name];
  if (named !== undefined) return named;
  if (/^[a-z]$/i.test(name)) {
    return { code: `Key${name.toUpperCase()}`, display: name.toUpperCase() };
  }
  if (/^[0-9]$/.test(name)) return { code: `Digit${name}`, display: name };
  if (name.startsWith('F') && /^F[0-9]{1,2}$/.test(name)) {
    return { code: name, display: name };
  }
  return undefined;
}

/** parseCombo turns 'Mod+Shift+[' into the pieces a matcher needs. */
export function parseCombo(combo: string): ParsedCombo | undefined {
  const parts = combo.split('+');
  const key = parts.pop();
  if (key === undefined) return undefined;
  const info = keyInfo(key);
  if (info === undefined) return undefined;
  const parsed: ParsedCombo = {
    mod: false,
    ctrl: false,
    alt: false,
    shift: false,
    code: info.code,
    display: info.display,
  };
  for (const part of parts) {
    switch (part.toLowerCase()) {
      case 'mod':
        parsed.mod = true;
        break;
      case 'ctrl':
        parsed.ctrl = true;
        break;
      case 'alt':
        parsed.alt = true;
        break;
      case 'shift':
        parsed.shift = true;
        break;
      default:
        return undefined;
    }
  }
  return parsed;
}

/** formatCombo renders one combo for the current platform. */
export function formatCombo(combo: string, isMac: boolean): string {
  const parsed = parseCombo(combo);
  if (parsed === undefined) return combo;
  const parts: string[] = [];
  if (isMac) {
    // macOS convention: control, option, shift, command, then the key.
    if (parsed.ctrl) parts.push('⌃');
    if (parsed.alt) parts.push('⌥');
    if (parsed.shift) parts.push('⇧');
    if (parsed.mod) parts.push('⌘');
    return [...parts, parsed.display].join('');
  }
  // Elsewhere Mod is Control, so the two share a slot.
  if (parsed.mod || parsed.ctrl) parts.push('Ctrl');
  if (parsed.alt) parts.push('Alt');
  if (parsed.shift) parts.push('Shift');
  return [...parts, parsed.display].join('+');
}

/** formatCombos renders every combo of a row, joined for the sheet. */
export function formatCombos(combos: string[], isMac: boolean): string {
  return combos.map((combo) => formatCombo(combo, isMac)).join(' / ');
}

/** The subset of a KeyboardEvent the matcher reads (plus the fields the
 * IME guard in lib/ime.ts reads, which resolveShortcut consults first). */
export interface ShortcutEvent extends IMEKeyEvent {
  code: string;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  repeat: boolean;
}

export interface ShortcutContext {
  isMac: boolean;
  /** An overlay layer currently owns the keyboard (lib/overlay.ts). */
  overlayOpen: boolean;
  /**
   * The event target is a text field or a contenteditable surface. A spec
   * may still fire there (see ShortcutSpec.editable), but only for the
   * combos it lists.
   */
  editable: boolean;
}

/**
 * comboRunsInFields reports whether this combo may fire while a text
 * surface has the caret. The two forms of ShortcutSpec.editable differ
 * per combo, which is the whole reason the check lives here rather than
 * at the spec level in resolveShortcut.
 */
function comboRunsInFields(spec: ShortcutSpec, combo: string): boolean {
  if (spec.editable === true) return true;
  if (Array.isArray(spec.editable)) return spec.editable.includes(combo);
  return false;
}

/** matchesCombo reports whether an event is exactly this combo. */
export function matchesCombo(
  event: ShortcutEvent,
  combo: string,
  isMac: boolean,
): boolean {
  const parsed = parseCombo(combo);
  if (parsed === undefined) return false;
  if (event.code !== parsed.code) return false;
  if (event.shiftKey !== parsed.shift) return false;
  if (event.altKey !== parsed.alt) return false;
  // Mod is Command on macOS and Control elsewhere, and the other one must
  // not be held: on macOS ⌃N/⌃P/⌃A/⌃E/⌃K are the text fields' Emacs-style
  // caret motions, so accepting either modifier would eat them.
  if (isMac) {
    if (event.metaKey !== parsed.mod) return false;
    if (event.ctrlKey !== parsed.ctrl) return false;
  } else {
    if (event.ctrlKey !== (parsed.mod || parsed.ctrl)) return false;
    if (event.metaKey) return false;
  }
  return true;
}

/**
 * resolveShortcut picks the shortcut an event should run, or undefined.
 *
 * The dispatcher this feeds is the window's *capture*-phase listener: it
 * sees the key before the focused surface does, which is how ⌘K reaches
 * the palette while the composer holds the caret. Everything that should
 * take precedence over it therefore has to be checked here rather than
 * left to the event's natural order — the overlay stack (Escape) and the
 * IME are the two that matter.
 */
export function resolveShortcut(
  shortcuts: ShortcutSpec[],
  event: ShortcutEvent,
  ctx: ShortcutContext,
): ShortcutSpec | undefined {
  // The IME owns its keys (lib/ime.ts): mid-composition the candidate
  // window needs Enter/Escape, and the stray keydown Chromium delivers
  // after a composition ends — looking like a real press — must not run a
  // command either.
  if (imeKeyOwner(event) !== null) return undefined;
  for (const spec of shortcuts) {
    if (event.repeat && spec.repeat !== true) continue;
    if (ctx.overlayOpen && spec.overlay !== 'runs') continue;
    const combo = spec.combos.find((candidate) =>
      matchesCombo(event, candidate, ctx.isMac),
    );
    if (combo === undefined) continue;
    // A combo a text surface keeps to itself ends the search: no other
    // spec binds it, and the surface that owns the key has to be the one
    // that sees it.
    if (ctx.editable && !comboRunsInFields(spec, combo)) return undefined;
    return spec;
  }
  return undefined;
}

/**
 * isEditableTarget reports whether a text surface owns the caret, so a
 * shortcut can decide to stay out of its way. The composer is a
 * contenteditable ProseMirror node, which is why the check cannot rely on
 * tag names alone.
 */
export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  // isContentEditable covers a node nested inside the editor; the
  // attribute is what the editor element itself carries in environments
  // that do not implement the inherited property (jsdom).
  if (target.isContentEditable || target.contentEditable === 'true')
    return true;
  if (target.tagName === 'TEXTAREA' || target.tagName === 'SELECT') return true;
  if (target.tagName !== 'INPUT') return false;
  const type = (target as HTMLInputElement).type;
  return type !== 'button' && type !== 'checkbox' && type !== 'radio';
}
