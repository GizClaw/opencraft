import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from './i18n';
import { Events, System } from '@wailsio/runtime';
import { ChatView } from './components/ChatView';
import { Sidebar } from './components/Sidebar';
import { StatusBar } from './components/StatusBar';
import { SubagentDock } from './components/SubagentDock';
import { TopBar } from './components/TopBar';
import { Toaster } from './components/Toaster';
import { WelcomeView } from './components/WelcomeView';
import { useStore } from './lib/store';
import { usePluginStore } from './plugins/store';
import type { UIEvent } from './lib/types';
import { api } from './lib/api';

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
  const workspace = useStore((s) => s.workspace);
  const openDraftChat = useStore((s) => s.openDraftChat);
  const openConfig = useStore((s) => s.openConfig);
  const openFiles = useStore((s) => s.openFiles);
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
    let removeDebugContextMenu: (() => void) | null = null;
    void System.Environment().then((env) => {
      if (!alive) return;
      setIsMac(env.OS === 'darwin');
      // Wails v3 dev/debug builds always enable the native webview
      // context menu, which exposes Reload + Inspect Element to users.
      // Keep the app menu clean outside production; React's own
      // right-click menus still work because they prevent the default
      // themselves and are not cancelled by this listener.
      if (env.Debug) {
        const onContextMenu = (event: MouseEvent) => {
          if (!event.defaultPrevented) event.preventDefault();
        };
        window.addEventListener('contextmenu', onContextMenu, true);
        removeDebugContextMenu = () =>
          window.removeEventListener('contextmenu', onContextMenu, true);
      }
    });
    return () => {
      alive = false;
      removeDebugContextMenu?.();
    };
  }, [init]);

  // Keep the native tray menu and exit dialog in the same language as
  // the UI: report the detected language once and on every change.
  useEffect(() => {
    const syncLanguage = () => {
      const language = i18n.language?.startsWith('zh') ? 'zh' : 'en';
      void api.setLanguage(language).catch(() => {
        // Native language sync is best-effort; the UI still works.
      });
    };
    syncLanguage();
    i18n.on('languageChanged', syncLanguage);
    return () => {
      i18n.off('languageChanged', syncLanguage);
    };
  }, []);

  useEffect(() => {
    // Load installed plugins once the shell mounts; the plugin host
    // registers its settings panels and sidebar entries afterwards.
    void usePluginStore.getState().load();
    const off = Events.On('opencraft:ui', (e) => {
      const ev = e.data as UIEvent;
      // Interact prompts and finished turns still reach handleEvent below;
      // their system notifications are raised Go-side so hidden windows do
      // not lose them.
      if (ev.type === 'turn_end') {
        // Flush any deltas still waiting on the stream coalescer so the
        // transcript settles. The system notification for finished turns
        // is raised Go-side from the same event.
        useStore.getState().flushStreams();
      }
      handleEvent(ev);
    });
    return off;
  }, [init, handleEvent]);

  // Feed the pet mind coarse "user is around" pulses from the main
  // window. Throttled to 2s; the pet only needs to notice presence,
  // not every mouse move.
  useEffect(() => {
    let last = 0;
    const report = () => {
      const now = Date.now();
      if (now - last < 2000) return;
      last = now;
      void api.reportUserActivity();
    };
    window.addEventListener('pointermove', report);
    window.addEventListener('keydown', report);
    return () => {
      window.removeEventListener('pointermove', report);
      window.removeEventListener('keydown', report);
    };
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!e.metaKey && !e.ctrlKey) return;
      const key = e.key.toLowerCase();
      if (key === 'n') {
        e.preventDefault();
        void openDraftChat();
      } else if (key === ',') {
        e.preventDefault();
        openConfig();
      } else if (key === 'o') {
        e.preventDefault();
        openFiles();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [openDraftChat, openConfig, openFiles]);

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
        ) : workspace ? (
          <ChatView />
        ) : (
          <WelcomeView />
        )}
      </div>
      {workspace && !toolsView && <SubagentDock />}
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
