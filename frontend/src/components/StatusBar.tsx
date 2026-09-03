import { Loader2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useStore } from '../lib/store';
import { useConversationState, useFocusState } from '../state/react';

export function StatusBar() {
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
  const { t } = useTranslation();

  return (
    <footer className="h-8 shrink-0 border-t border-edge bg-panel flex items-center gap-3 px-4 text-xs text-dim">
      <span className="flex items-center gap-1.5 min-w-0">
        {busy && (
          <Loader2 size="0.9286rem" className="animate-spin text-accent" />
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
        <span className="rounded bg-panel2 border border-edge px-2 py-0.5 whitespace-nowrap">
          {model}
        </span>
      )}
    </footer>
  );
}
