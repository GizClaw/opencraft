import { Loader2, Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { formatCombos, shortcutByID } from '../lib/keys';
import { useStore } from '../lib/store';
import { useConversationState, useFocusState } from '../state/react';
import { ICON } from './ui/icon';

// The status bar is the one strip that is always on screen, so the ⌘K
// affordance lives here: the palette itself is only discoverable if
// something says it exists. The badge is the key table's own spelling of
// the combo (⌘K on macOS, Ctrl+K elsewhere), so it cannot drift from the
// key that opens the palette.
const PALETTE = shortcutByID('palette.open');

export function StatusBar({ isMac }: { isMac: boolean }) {
  const focus = useFocusState();
  const conversation = useConversationState(
    focus.name === 'active' ? focus.sessionID : undefined,
  );
  const busy =
    conversation?.turn.name === 'starting' ||
    conversation?.turn.name === 'running';
  const statusText = useStore((s) => s.statusText);
  const lastUsage = useStore((s) => s.lastUsage);
  const model = useStore((s) => s.status?.default_model) ?? '';
  const openPalette = useStore((s) => s.openPalette);
  const { t } = useTranslation();

  return (
    <footer className="h-8 shrink-0 border-t border-edge bg-panel flex items-center gap-3 px-4 text-xs text-dim">
      <span className="flex items-center gap-1.5 min-w-0">
        {busy && (
          <Loader2 size={ICON.sm} className="animate-spin text-accent" />
        )}
        <span className="truncate">
          {statusText || (busy ? t('status.running') : t('status.ready'))}
        </span>
      </span>
      <span className="flex-1" />
      {lastUsage && (
        <span className="tabular-nums whitespace-nowrap">
          ↑{lastUsage.input_tokens}
          {lastUsage.cache_read_tokens > 0 &&
            `(${lastUsage.cache_read_tokens})`}{' '}
          ↓{lastUsage.output_tokens}
          {lastUsage.reasoning_tokens > 0 && (
            <>
              {' '}
              {t('status.thinking')} {lastUsage.reasoning_tokens}
            </>
          )}{' '}
          · {lastUsage.latency_ms}ms
        </span>
      )}
      {model && (
        <span className="rounded-tight bg-panel2 border border-edge px-2 py-0.5 whitespace-nowrap">
          {model}
        </span>
      )}
      <button
        type="button"
        onClick={openPalette}
        data-tip={t('palette.title')}
        className="flex shrink-0 items-center gap-1.5 rounded-tight border border-edge px-2 py-0.5 whitespace-nowrap transition-colors hover:bg-panel2 hover:text-fg"
      >
        <Search size={ICON.xs} />
        {t('palette.title')}
        {PALETTE !== undefined && (
          <kbd className="rounded-tight bg-panel2 px-1 text-micro text-faint">
            {formatCombos(PALETTE.combos, isMac)}
          </kbd>
        )}
      </button>
    </footer>
  );
}
