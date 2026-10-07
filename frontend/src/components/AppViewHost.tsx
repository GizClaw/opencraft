import { MessagesSquare, PackageOpen } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { ErrorBoundary } from './ErrorBoundary';
import { ICON } from './ui/icon';
import type { LiveAppScope } from '../apps/host';

/**
 * AppViewHost renders one application's own surface: the tab bar (its
 * registered views, in the order they asked for, plus the conversation
 * that always exists) and the selected tab's content.
 *
 * The built-in conversation is not a view an application registers — it
 * is the floor of the page, and it is what a view's failure falls back
 * to. Every view renders inside its own boundary, so a bundle that
 * throws while rendering shows its error in place of its tab instead of
 * taking the window's tree down with it (React would rebuild from the
 * nearest boundary above, remounting the whole page).
 */
export function AppViewHost({
  scope,
  tab,
  onTab,
  children,
}: {
  scope: LiveAppScope;
  /** tab is the selected tab id; 'chat' is the built-in conversation. */
  tab: string;
  onTab: (id: string) => void;
  /** children is the built-in conversation, rendered when selected. */
  children: React.ReactNode;
}) {
  const { t } = useTranslation();
  const views = scope.views ?? [];
  const active = views.find((v) => v.id === tab);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div
        role="tablist"
        aria-label={t('apps.tabs')}
        className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-edge px-2"
      >
        <TabButton
          id="chat"
          label={t('apps.chat.tab')}
          icon={<MessagesSquare size={ICON.sm} />}
          active={tab === 'chat'}
          onClick={() => onTab('chat')}
        />
        {views.map((view) => (
          <TabButton
            key={view.id}
            id={view.id}
            label={view.title}
            active={tab === view.id}
            onClick={() => onTab(view.id)}
          />
        ))}
      </div>
      {scope.error && tab === 'chat' ? (
        // The bundle failed to apply. The page stays usable — the
        // conversation is untouched by a frontend failure — and the raw
        // error is printed for the author, who is the only one who can
        // act on it.
        <div
          data-testid="app-view-error"
          className="mx-4 mt-3 flex items-start gap-2 rounded-card border border-err/40 bg-err/5 px-3 py-2 text-xs"
        >
          <PackageOpen size={ICON.sm} className="mt-0.5 shrink-0 text-err" />
          <div className="min-w-0">
            <p className="font-medium text-err">{t('apps.viewFailed')}</p>
            <p className="mt-1 break-words text-dim">{scope.error}</p>
          </div>
        </div>
      ) : null}
      {active ? (
        <ErrorBoundary scope={`app:${scope.id}`} inline>
          <active.Component />
        </ErrorBoundary>
      ) : (
        children
      )}
    </div>
  );
}

function TabButton({
  id,
  label,
  icon,
  active,
  onClick,
}: {
  id: string;
  label: string;
  icon?: React.ReactNode;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      role="tab"
      aria-selected={active}
      data-testid={`app-tab-${id}`}
      onClick={onClick}
      className={`my-1 flex shrink-0 items-center gap-1.5 rounded-tight px-2 py-1 text-xs transition-colors ${
        active ? 'bg-panel3 text-fg' : 'text-dim hover:bg-panel2 hover:text-fg'
      }`}
    >
      {icon}
      {label}
    </button>
  );
}
