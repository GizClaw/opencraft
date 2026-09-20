import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Search, SearchX } from 'lucide-react';
import { useStore } from '../lib/store';
import { buildCommands, filterCommands, type Command } from '../lib/commands';
import { EmptyState } from './ui/EmptyState';
import { Overlay } from './ui/Overlay';
import { ICON } from './ui/icon';

// CommandPalette — the ⌘K surface: jump to a workspace, a session, a
// settings tab, or run a plain action without hunting through the UI.
//
// It is an overlay on the shared shell (scrim, escape ownership, focus
// trap, animation), so it stacks correctly with the settings page and the
// tools panel instead of fighting them for the Escape key. Focus stays in
// the input and the list is navigated with aria-activedescendant, which
// keeps typing and moving through results in the same keystroke stream.
export function CommandPalette() {
  const open = useStore((s) => s.paletteOpen);
  const closePalette = useStore((s) => s.closePalette);
  const { t } = useTranslation();

  const theme = useStore((s) => s.theme);
  const workspace = useStore((s) => s.workspace);
  const workspaces = useStore((s) => s.workspaces);
  const sessions = useStore((s) => s.sessions);
  const newChat = useStore((s) => s.newChat);
  const openConfig = useStore((s) => s.openConfig);
  const openTools = useStore((s) => s.openTools);
  const openFiles = useStore((s) => s.openFiles);
  const chooseWorkspace = useStore((s) => s.chooseWorkspace);
  const openWorkspace = useStore((s) => s.openWorkspace);
  const openSessionInWorkspace = useStore((s) => s.openSessionInWorkspace);
  const setTheme = useStore((s) => s.setTheme);

  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLUListElement | null>(null);

  const commands = useMemo(
    () =>
      buildCommands({
        t,
        actions: {
          newChat: () => void newChat(),
          openConfig,
          openTools,
          openFiles,
          chooseWorkspace: () => void chooseWorkspace(),
          openWorkspace: (path) => void openWorkspace(path),
          openSessionInWorkspace: (id, path) =>
            void openSessionInWorkspace(id, path),
          setTheme,
        },
        state: { theme, workspace, workspaces, sessions },
      }),
    [
      t,
      theme,
      workspace,
      workspaces,
      sessions,
      newChat,
      openConfig,
      openTools,
      openFiles,
      chooseWorkspace,
      openWorkspace,
      openSessionInWorkspace,
      setTheme,
    ],
  );

  const results = useMemo(
    () => filterCommands(commands, query),
    [commands, query],
  );

  // Every open starts clean: a stale query would answer a question the user
  // is no longer asking.
  useEffect(() => {
    if (open) {
      setQuery('');
      setActive(0);
    }
  }, [open]);

  useEffect(() => {
    setActive((current) =>
      results.length === 0 ? 0 : Math.min(current, results.length - 1),
    );
  }, [results]);

  // The active row follows the pointer only when the pointer is over the
  // list, so moving the mouse into the panel does not hijack the keyboard.
  useEffect(() => {
    const node = listRef.current?.querySelector('[data-active="true"]');
    node?.scrollIntoView({ block: 'nearest' });
  }, [active]);

  const run = useCallback(
    (command: Command | undefined) => {
      if (command === undefined) return;
      closePalette();
      command.run();
    },
    [closePalette],
  );

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (results.length === 0) return;
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      setActive((current) => (current + 1) % results.length);
      return;
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault();
      setActive((current) => (current - 1 + results.length) % results.length);
      return;
    }
    if (event.key === 'Home') {
      event.preventDefault();
      setActive(0);
      return;
    }
    if (event.key === 'End') {
      event.preventDefault();
      setActive(results.length - 1);
      return;
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      run(results[active]);
    }
  };

  let lastGroup = '';
  return (
    <Overlay
      open={open}
      onClose={closePalette}
      variant="top"
      initialFocus="[data-palette-input]"
      ariaLabel={t('palette.title')}
      panelClassName="flex max-h-[60vh] w-[36rem] max-w-full flex-col overflow-hidden rounded-card border border-edge bg-panel shadow-modal"
    >
      <div className="flex items-center gap-2 border-b border-edge px-3 py-2.5">
        <Search size={ICON.sm} className="shrink-0 text-dim" />
        <input
          data-palette-input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={onKeyDown}
          role="combobox"
          aria-expanded
          aria-controls="palette-results"
          aria-activedescendant={
            results.length > 0 ? `palette-option-${active}` : undefined
          }
          aria-label={t('palette.placeholder')}
          placeholder={t('palette.placeholder')}
          className="min-w-0 flex-1 bg-transparent text-sm outline-none"
        />
        <kbd className="shrink-0 rounded-tight border border-edge bg-panel2 px-1.5 py-0.5 text-micro text-faint">
          esc
        </kbd>
      </div>

      {results.length === 0 ? (
        <EmptyState
          icon={SearchX}
          title={t('palette.empty')}
          hint={t('palette.emptyHint')}
        />
      ) : (
        <ul
          ref={listRef}
          id="palette-results"
          role="listbox"
          aria-label={t('palette.title')}
          className="min-h-0 flex-1 overflow-y-auto py-1"
        >
          {results.map((command, index) => {
            const Icon = command.icon;
            const isActive = index === active;
            const header =
              command.group === lastGroup ? null : t(command.group);
            lastGroup = command.group;
            return (
              <li key={command.id} className="list-none">
                {header !== null && (
                  <div className="px-3 pb-1 pt-2 text-micro uppercase tracking-wide text-faint">
                    {header}
                  </div>
                )}
                <div
                  id={`palette-option-${index}`}
                  role="option"
                  aria-selected={isActive}
                  data-active={isActive}
                  onMouseMove={() => setActive(index)}
                  onClick={() => run(command)}
                  className={`flex cursor-default items-center gap-2.5 px-3 py-1.5 ${
                    isActive ? 'bg-panel2' : ''
                  }`}
                >
                  <Icon
                    size={ICON.sm}
                    className={
                      isActive ? 'shrink-0 text-accent' : 'shrink-0 text-faint'
                    }
                  />
                  <span className="min-w-0 flex-1 truncate text-sm text-fg">
                    {command.title}
                  </span>
                  {command.hint !== undefined && command.hint !== '' && (
                    <span className="max-w-[45%] shrink-0 truncate text-micro text-faint">
                      {command.hint}
                    </span>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <div className="flex shrink-0 items-center gap-3 border-t border-edge px-3 py-1.5 text-micro text-faint">
        <span>{t('palette.footer')}</span>
      </div>
    </Overlay>
  );
}
