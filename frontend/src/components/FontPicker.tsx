import { Check, ChevronDown, Search } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import {
  FONT_PRESET_CUSTOM,
  quoteFontFamily,
  type FontPreset,
} from '../lib/appearance';
import { ICON } from './ui/icon';

// FontPicker is the Settings > Interface font control: the built-in presets
// plus the families the host reports (internal/foundation/sysfont), in one
// searchable menu whose rows are drawn in their own face.
//
// The menu is portaled to the document body so the settings modal's scroll
// container cannot clip it. It hangs off the trigger's right edge because the
// controls sit at the card's right edge — a left-anchored menu would overhang
// the dialog. It closes on outside click, Escape, scroll or resize, the same
// contract as MenuSelect.
//
// The search field is IME-aware: filtering pauses while a composition is in
// flight and Enter/arrow handling is skipped, so confirming a pinyin/kana
// candidate can never pick a font in the middle of the composition.

/** One selectable entry once the catalogue and the query are applied. */
type FontRow =
  | { kind: 'preset'; id: string; label: string; stack: string }
  | { kind: 'family'; family: string };

interface MenuAnchor {
  top: number;
  right: number;
  width: number;
  maxHeight: number;
}

export function FontPicker({
  label,
  presets,
  families,
  presetId,
  familyName,
  onChange,
}: {
  /** Accessible name of the control, also used by tests. */
  label: string;
  /** Built-in presets, listed above the installed families. */
  presets: FontPreset[];
  /** Installed families; null while the host list is still loading. */
  families: string[] | null;
  presetId: string;
  familyName: string;
  onChange: (presetId: string, familyName: string) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [composing, setComposing] = useState(false);
  const [active, setActive] = useState(0);
  const [anchor, setAnchor] = useState<MenuAnchor | null>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  const preset = presets.find((candidate) => candidate.id === presetId);
  const named = preset === undefined;
  const activeFamily = familyName.trim();

  const filtered = useMemo(() => {
    if (families === null) return [];
    // A composition is not a search term yet: filtering mid-composition
    // re-renders the list under the input method and can swallow the text
    // being composed.
    if (composing) return families;
    const needle = query.trim().toLowerCase();
    if (needle === '') return families;
    return families.filter((family) => family.toLowerCase().includes(needle));
  }, [composing, families, query]);

  const rows = useMemo<FontRow[]>(
    () => [
      ...presets.map((candidate) => ({
        kind: 'preset' as const,
        id: candidate.id,
        label: t(candidate.labelKey),
        stack: candidate.stack,
      })),
      ...filtered.map((family) => ({ kind: 'family' as const, family })),
    ],
    [filtered, presets, t],
  );

  // Keep the highlight inside the list as the query narrows it.
  useEffect(() => {
    setActive((current) =>
      Math.min(Math.max(current, 0), Math.max(rows.length - 1, 0)),
    );
  }, [rows.length]);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onPointerDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (menuRef.current?.contains(target)) return;
      if (triggerRef.current?.contains(target)) return;
      close();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      // While a composition is running Escape cancels the candidate: the
      // input method owns it, not the menu.
      if (event.key !== 'Escape' || event.isComposing) return;
      // Escape belongs to the open menu first: stopping here keeps the
      // settings dialog (a window-level listener) from closing underneath.
      event.stopPropagation();
      close();
    };
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    // A scroll or resize invalidates the measured anchor. The menu's own list
    // scrolls independently — resting a trackpad on it must not close the menu
    // it is scrolling.
    const onScroll = (event: Event) => {
      const target = event.target;
      if (target instanceof Node && menuRef.current?.contains(target)) return;
      close();
    };
    window.addEventListener('scroll', onScroll, true);
    window.addEventListener('resize', close);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
      window.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('resize', close);
    };
  }, [open]);

  const toggle = () => {
    const trigger = triggerRef.current;
    if (trigger === null) return;
    const box = trigger.getBoundingClientRect();
    const spaceBelow = window.innerHeight - box.bottom - 12;
    setAnchor({
      top: box.bottom + 4,
      right: box.right,
      width: Math.max(box.width, 240),
      // Stay inside the window: a shorter list still scrolls, and the search
      // field above it stays reachable.
      maxHeight: Math.max(160, Math.min(352, spaceBelow)),
    });
    setQuery('');
    setComposing(false);
    setActive(0);
    setOpen((wasOpen) => !wasOpen);
  };

  const pick = (row: FontRow) => {
    setOpen(false);
    if (row.kind === 'preset') {
      onChange(row.id, '');
      return;
    }
    onChange(FONT_PRESET_CUSTOM, row.family);
  };

  const onSearchKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    // The input method owns the keyboard until its candidate is committed;
    // keyCode 229 covers the engines that do not report isComposing.
    if (event.nativeEvent.isComposing || event.keyCode === 229) return;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const delta = event.key === 'ArrowDown' ? 1 : -1;
      setActive((current) =>
        Math.min(Math.max(current + delta, 0), rows.length - 1),
      );
      return;
    }
    if (event.key === 'Enter' && rows[active] !== undefined) {
      event.preventDefault();
      pick(rows[active]);
    }
  };

  const isSelected = (row: FontRow): boolean =>
    row.kind === 'preset'
      ? preset?.id === row.id
      : named && activeFamily === row.family;

  // The trigger names whatever is selected: the preset's own label, the
  // picked family, or — for a document that lost its family — the first
  // preset, which is the stack the renderer falls back to.
  const placeholder = presets[0] !== undefined ? t(presets[0].labelKey) : '';
  const triggerLabel =
    preset !== undefined
      ? t(preset.labelKey)
      : activeFamily !== ''
        ? activeFamily
        : placeholder;

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={toggle}
        className={`inline-flex h-[1.875rem] w-full items-center gap-1.5 rounded-control border bg-panel px-2 text-xs transition-colors outline-none hover:border-accent/60 focus:border-accent ${
          open ? 'border-accent' : 'border-edge'
        }`}
      >
        <span
          className="min-w-0 flex-1 truncate text-left text-fg"
          style={
            named && activeFamily !== ''
              ? { fontFamily: quoteFontFamily(activeFamily) }
              : undefined
          }
        >
          {triggerLabel}
        </span>
        <ChevronDown
          size={ICON.xs}
          className={`shrink-0 text-dim transition-transform ${
            open ? 'rotate-180' : ''
          }`}
        />
      </button>
      {open &&
        anchor !== null &&
        createPortal(
          <div
            ref={menuRef}
            style={{
              top: anchor.top,
              right: Math.max(window.innerWidth - anchor.right, 8),
              width: anchor.width,
              maxHeight: anchor.maxHeight,
            }}
            className="fixed z-[100] flex flex-col overflow-hidden rounded-card border border-edge bg-panel shadow-popover"
          >
            <label className="flex items-center gap-1.5 border-b border-edge px-2 py-1.5">
              <Search size={ICON.xs} className="shrink-0 text-dim" />
              <input
                type="text"
                autoFocus
                aria-label={t('config.uiFontSearch')}
                value={query}
                placeholder={t('config.uiFontSearch')}
                spellCheck={false}
                onChange={(event) => setQuery(event.target.value)}
                onCompositionStart={() => setComposing(true)}
                onCompositionEnd={(event) => {
                  setComposing(false);
                  setQuery(event.currentTarget.value);
                }}
                onKeyDown={onSearchKeyDown}
                className="min-w-0 flex-1 bg-transparent text-xs text-fg outline-none placeholder:text-dim"
              />
            </label>
            <div
              role="listbox"
              aria-label={label}
              className="min-h-0 flex-1 overflow-y-auto py-1"
            >
              {rows.map((row, index) => {
                const selected = isSelected(row);
                return (
                  <button
                    key={
                      row.kind === 'preset'
                        ? `preset:${row.id}`
                        : `family:${row.family}`
                    }
                    type="button"
                    role="option"
                    aria-selected={selected}
                    onMouseEnter={() => setActive(index)}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => pick(row)}
                    className={`flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs ${
                      active === index ? 'bg-panel2' : ''
                    } ${selected ? 'text-accent' : 'text-fg'}`}
                  >
                    <Check
                      size={ICON.xs}
                      className={`shrink-0 ${
                        selected ? 'text-accent' : 'invisible'
                      }`}
                    />
                    <span
                      className="min-w-0 flex-1 truncate"
                      style={
                        row.kind === 'family'
                          ? { fontFamily: quoteFontFamily(row.family) }
                          : { fontFamily: row.stack }
                      }
                    >
                      {row.kind === 'preset' ? row.label : row.family}
                    </span>
                  </button>
                );
              })}
              {families === null && (
                <p className="px-2.5 py-1.5 text-xs text-dim">
                  {t('config.uiFontLoading')}
                </p>
              )}
              {families !== null && filtered.length === 0 && (
                <p className="px-2.5 py-1.5 text-xs text-dim">
                  {families.length === 0
                    ? t('config.uiFontUnavailable')
                    : t('config.uiFontNoMatch')}
                </p>
              )}
            </div>
          </div>,
          document.body,
        )}
    </>
  );
}
