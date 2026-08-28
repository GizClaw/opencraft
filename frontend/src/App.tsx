import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from './i18n';
import {
  EventsOn,
  InitializeNotifications,
  RequestNotificationAuthorization,
  SendNotification,
} from '../wailsjs/runtime/runtime';
import { ChatView } from './components/ChatView';
import { Sidebar } from './components/Sidebar';
import { StatusBar } from './components/StatusBar';
import { SubagentSidebar } from './components/SubagentSidebar';
import { Toaster } from './components/Toaster';
import { useStore } from './lib/store';
import { usePluginStore } from './plugins/store';
import type { UIEvent } from './lib/types';

const ConfigPage = lazy(() =>
  import('./components/ConfigPage').then((m) => ({ default: m.ConfigPage })),
);
const ToolsPanel = lazy(() =>
  import('./components/ToolsPanel').then((m) => ({ default: m.ToolsPanel })),
);

export default function App() {
  const init = useStore((s) => s.init);
  const handleEvent = useStore((s) => s.handleEvent);
  const status = useStore((s) => s.status);
  const fatal = useStore((s) => s.fatal);
  const configOpen = useStore((s) => s.configOpen);
  const toolsView = useStore((s) => s.toolsView);
  const current = useStore((s) => s.current);
  const subagentCards = useStore((s) => s.subagentCards);
  const subagentPanelOpen = useStore((s) => s.subagentPanelOpen);
  const loadSubagentCards = useStore((s) => s.loadSubagentCards);
  const newChat = useStore((s) => s.newChat);
  const openConfig = useStore((s) => s.openConfig);
  const openTools = useStore((s) => s.openTools);
  const { t } = useTranslation();
  const [sidebarW, setSidebarW] = useState(
    () => Number(localStorage.getItem('oc.sidebarW')) || 240,
  );
  // Cleanup for an in-flight sidebar drag when the tree changes
  // mid-drag, so the window listeners never leak past the component.
  const dragCleanup = useRef<(() => void) | null>(null);
  useEffect(() => () => dragCleanup.current?.(), []);

  useEffect(() => {
    void init();
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
        const data = ev.data as { status: string };
        const body =
          data.status === 'completed'
            ? i18n.t('notify.done')
            : data.status === 'failed' || data.status === 'aborted'
              ? i18n.t('notify.failed')
              : data.status;
        void SendNotification({ id: 'turn-end', title: 'OpenCraft', body });
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
        openTools('kanban');
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [newChat, openConfig, openTools]);

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
      <div className="flex-1 flex min-h-0">
        <div style={{ width: sidebarW }} className="shrink-0">
          <Sidebar />
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
            <ChatView />
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
