import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from './i18n';
import { Events, System } from '@wailsio/runtime';
import { ChatView } from './components/ChatView';
import { Sidebar } from './components/Sidebar';
import { SidebarResizeHandle } from './components/SidebarResizeHandle';
import { StatusBar } from './components/StatusBar';
import { SubagentDock } from './components/SubagentDock';
import { TopBar } from './components/TopBar';
import { Toaster } from './components/Toaster';
import { TooltipLayer } from './components/ui/Tooltip';
import { WelcomeView } from './components/WelcomeView';
import { CommandPalette } from './components/CommandPalette';
import { ShortcutSheet } from './components/ShortcutSheet';
import { useStore } from './lib/store';
import { useShellCommands } from './lib/shellCommands';
import { useShortcuts } from './lib/useShortcuts';
import { readSidebarWidth } from './lib/sidebarWidth';
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
  const { t } = useTranslation();
  const [sidebarW, setSidebarW] = useState(readSidebarWidth);
  // The settings page is a lazy chunk that outlives its own close: it stays
  // mounted once opened so its exit animation can play, and so reopening it
  // is instant. First open still loads the chunk.
  const [settingsMounted, setSettingsMounted] = useState(false);
  useEffect(() => {
    if (configOpen) setSettingsMounted(true);
  }, [configOpen]);
  // Platform is known synchronously from the user agent so the
  // Windows/Linux top bar never flashes on macOS (or vice versa);
  // Environment() reconciles the canonical value right after.
  const [isMac, setIsMac] = useState(() =>
    /Macintosh|Mac OS X/i.test(navigator.userAgent),
  );
  // Every keyboard command in the app comes through here — the window
  // listener below, the command palette, and the native menu's bridge.
  const runShortcut = useShellCommands();
  useShortcuts(runShortcut, isMac);

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

  // The native menu (macOS) is a second front for the commands the shell
  // already owns: an item bridges its shortcut id here instead of running
  // anything itself, so a menu click and its key equivalent land in the
  // same place. The ref keeps the subscription stable while `runShortcut`
  // is rebuilt for fresh turn state.
  const runShortcutRef = useRef(runShortcut);
  runShortcutRef.current = runShortcut;
  useEffect(() => {
    return Events.On('opencraft:menu', (e) => {
      const data = e.data as { command?: unknown } | null | undefined;
      const command = data?.command;
      if (typeof command === 'string' && command !== '') {
        runShortcutRef.current(command);
      }
    });
  }, []);

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

  if (fatal) {
    return (
      <div className="h-full grid place-items-center">
        <div className="max-w-md rounded-card border border-err/40 bg-panel p-6 text-sm">
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
        {/* The seam between the two columns, and the only control that
            moves it. */}
        <SidebarResizeHandle value={sidebarW} onChange={setSidebarW} />
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
      <StatusBar isMac={isMac} />
      {settingsMounted && (
        <Suspense fallback={null}>
          <ConfigPage />
        </Suspense>
      )}
      <Toaster />
      <CommandPalette isMac={isMac} runShortcut={runShortcut} />
      <ShortcutSheet isMac={isMac} />
      <TooltipLayer isMac={isMac} />
    </div>
  );
}
