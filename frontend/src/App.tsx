import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from './i18n';
import {
  Environment,
  EventsOn,
  InitializeNotifications,
  RequestNotificationAuthorization,
  SendNotification,
} from '../wailsjs/runtime/runtime';
import { ChatView } from './components/ChatView';
import { Sidebar } from './components/Sidebar';
import { StatusBar } from './components/StatusBar';
import { SubagentSidebar } from './components/SubagentSidebar';
import { TopBar } from './components/TopBar';
import { Toaster } from './components/Toaster';
import { WelcomeView } from './components/WelcomeView';
import { useStore, type AssistantItem } from './lib/store';
import { usePluginStore } from './plugins/store';
import type { UIEvent } from './lib/types';

const ConfigPage = lazy(() =>
  import('./components/ConfigPage').then((m) => ({ default: m.ConfigPage })),
);
const ToolsPanel = lazy(() =>
  import('./components/ToolsPanel').then((m) => ({ default: m.ToolsPanel })),
);

// Notification copy limits: macOS banners truncate long text, so the
// turn-end notification keeps the session title and the agent's final
// output short enough to read at a glance.
const maxNotifyTitle = 80;
const maxNotifySnippet = 160;

function truncate(text: string, max: number): string {
  if (text.length <= max) return text;
  return `${text.slice(0, max)}…`;
}

// turnEndNotification builds the macOS notification for one finished
// turn: the title is the session title and the body is the status plus
// the agent's latest text output (truncated), so the banner says more
// than just "task finished".
function turnEndNotification(data: {
  run_id?: string;
  conversation_id?: string;
  status: string;
}) {
  const state = useStore.getState();
  const convID =
    (data.run_id && state.runConvs[data.run_id]) || data.conversation_id;
  const statusText =
    data.status === 'completed'
      ? i18n.t('notify.done')
      : data.status === 'failed' || data.status === 'aborted'
        ? i18n.t('notify.failed')
        : data.status;

  // Session title: prefer the persisted/renamed title, fall back to
  // the conversation's first user message (the backend's auto-title
  // rule) and finally to the app name.
  let title = '';
  if (convID) {
    title =
      state.sessions.find((s) => s.id === convID)?.title ??
      state.conversations[convID]?.messages.find((m) => m.role === 'user')
        ?.text ??
      '';
  }
  title = truncate(title.trim(), maxNotifyTitle) || 'OpenCraft';

  // Latest agent output: the last assistant message accumulates every
  // text delta of the turn, so joining its text items yields the full
  // final answer.
  let snippet = '';
  if (convID) {
    const messages = state.conversations[convID]?.messages ?? [];
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i];
      if (m.role !== 'assistant') continue;
      snippet = m.items
        .filter(
          (it): it is AssistantItem & { kind: 'text' } => it.kind === 'text',
        )
        .map((it) => it.text)
        .join('')
        .trim();
      break;
    }
  }
  const body = snippet
    ? `${statusText}\n${truncate(snippet, maxNotifySnippet)}`
    : statusText;
  return { title, body };
}

export default function App() {
  const init = useStore((s) => s.init);
  const handleEvent = useStore((s) => s.handleEvent);
  const status = useStore((s) => s.status);
  const fatal = useStore((s) => s.fatal);
  const configOpen = useStore((s) => s.configOpen);
  const toolsView = useStore((s) => s.toolsView);
  const workspace = useStore((s) => s.workspace);
  const current = useStore((s) => s.current);
  const subagentCards = useStore((s) => s.subagentCards);
  const subagentPanelOpen = useStore((s) => s.subagentPanelOpen);
  const loadSubagentCards = useStore((s) => s.loadSubagentCards);
  const newChat = useStore((s) => s.newChat);
  const openConfig = useStore((s) => s.openConfig);
  const { t } = useTranslation();
  const [sidebarW, setSidebarW] = useState(
    () => Number(localStorage.getItem('oc.sidebarW')) || 240,
  );
  // Platform is known synchronously from the user agent so the
  // Windows/Linux top bar never flashes on macOS (or vice versa);
  // Environment() reconciles the canonical value right after.
  const [isMac, setIsMac] = useState(() =>
    /Macintosh|Mac OS X/i.test(navigator.userAgent),
  );
  // Cleanup for an in-flight sidebar drag when the tree changes
  // mid-drag, so the window listeners never leak past the component.
  const dragCleanup = useRef<(() => void) | null>(null);
  useEffect(() => () => dragCleanup.current?.(), []);

  useEffect(() => {
    void init();
    let alive = true;
    void Environment().then((env) => {
      if (alive) setIsMac(env.platform === 'darwin');
    });
    return () => {
      alive = false;
    };
  }, [init]);

  useEffect(() => {
    // Load installed plugins once the shell mounts; the plugin host
    // registers its settings panels and sidebar entries afterwards.
    void usePluginStore.getState().load();
    const off = EventsOn('opencraft:ui', (ev: UIEvent) => {
      if (ev.type === 'interact') {
        const spec = ev.data as { title?: string };
        void SendNotification({
          id: 'interact',
          title: 'OpenCraft',
          body: spec.title || i18n.t('notify.interact'),
        });
      } else if (ev.type === 'turn_end') {
        const data = ev.data as {
          run_id?: string;
          conversation_id?: string;
          status: string;
        };
        const { title, body } = turnEndNotification(data);
        void SendNotification({ id: 'turn-end', title, body });
      }
      handleEvent(ev);
    });
    return off;
  }, [init, handleEvent]);

  useEffect(() => {
    void InitializeNotifications()
      .then(() => RequestNotificationAuthorization())
      .catch(() => {
        // notifications are best-effort
      });
    const onKey = (e: KeyboardEvent) => {
      if (!e.metaKey && !e.ctrlKey) return;
      const key = e.key.toLowerCase();
      if (key === 'n') {
        e.preventDefault();
        void newChat();
      } else if (key === ',') {
        e.preventDefault();
        openConfig();
      } else if (key === 'k') {
        e.preventDefault();
        openConfig('kanban');
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [newChat, openConfig]);

  // Keep the current conversation's delegation list fresh; the right
  // sidebar appears as soon as the conversation spawns a subagent.
  useEffect(() => {
    if (!status) return;
    void loadSubagentCards();
    const timer = setInterval(() => void loadSubagentCards(), 2000);
    return () => clearInterval(timer);
  }, [status, current, loadSubagentCards]);

  const startDrag = () => (e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startW = sidebarW;
    const onMove = (ev: MouseEvent) => {
      const raw = startW + (ev.clientX - startX);
      const next = Math.min(480, Math.max(180, raw));
      setSidebarW(next);
      localStorage.setItem('oc.sidebarW', String(next));
    };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      dragCleanup.current = null;
    };
    dragCleanup.current = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  };

  if (fatal) {
    return (
      <div className="h-full grid place-items-center">
        <div className="max-w-md rounded-xl border border-err/40 bg-panel p-6 text-sm">
          <h2 className="font-semibold text-err mb-2">
            {t('app.startupFailed')}
          </h2>
          <p className="text-dim whitespace-pre-wrap break-all">
            {fatal || t('app.unknownError')}
          </p>
        </div>
      </div>
    );
  }

  if (!status) {
    return (
      <div className="h-full grid place-items-center text-dim text-sm">
        {t('app.starting')}
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      <TopBar isMac={isMac} />
      <div className="flex-1 flex min-h-0">
        <div style={{ width: sidebarW }} className="shrink-0">
          <Sidebar isMac={isMac} />
        </div>
        <div
          onMouseDown={startDrag()}
          className="w-1 shrink-0 cursor-col-resize bg-transparent hover:bg-accent/40"
        />
        {toolsView ? (
          <Suspense
            fallback={
              <div className="flex-1 grid place-items-center text-dim text-sm">
                {t('app.starting')}
              </div>
            }
          >
            <ToolsPanel />
          </Suspense>
        ) : (
          <>
            {workspace ? <ChatView /> : <WelcomeView />}
            {subagentPanelOpen && subagentCards.length > 0 && (
              <SubagentSidebar />
            )}
          </>
        )}
      </div>
      <StatusBar />
      {configOpen && (
        <Suspense fallback={null}>
          <ConfigPage />
        </Suspense>
      )}
      <Toaster />
    </div>
  );
}
