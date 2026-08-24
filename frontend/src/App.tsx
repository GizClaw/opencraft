import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "./i18n";
import {
  Environment,
  EventsOn,
  InitializeNotifications,
  RequestNotificationAuthorization,
  SendNotification,
  WindowToggleMaximise,
} from "../wailsjs/runtime/runtime";
import { ChatView } from "./components/ChatView";
import { ConfigPage } from "./components/ConfigPage";
import { KanbanView } from "./components/KanbanView";
import { Sidebar } from "./components/Sidebar";
import { StatusBar } from "./components/StatusBar";
import { useStore } from "./lib/store";
import type { UIEvent } from "./lib/types";

export default function App() {
  const init = useStore((s) => s.init);
  const handleEvent = useStore((s) => s.handleEvent);
  const status = useStore((s) => s.status);
  const fatal = useStore((s) => s.fatal);
  const configOpen = useStore((s) => s.configOpen);
  const kanbanOpen = useStore((s) => s.kanbanOpen);
  const newChat = useStore((s) => s.newChat);
  const openConfig = useStore((s) => s.openConfig);
  const openKanban = useStore((s) => s.openKanban);
  const { t } = useTranslation();
  const [platform, setPlatform] = useState("");
  const [sidebarW, setSidebarW] = useState(
    () => Number(localStorage.getItem("oc.sidebarW")) || 240,
  );

  useEffect(() => {
    void init();
    const off = EventsOn("opencraft:ui", (ev: UIEvent) => {
      if (ev.type === "interact") {
        const spec = ev.data as { title?: string };
        void SendNotification({
          id: "interact",
          title: "opencraft",
          body: spec.title || i18n.t("notify.interact"),
        });
      } else if (ev.type === "turn_end") {
        const data = ev.data as { status: string };
        const body =
          data.status === "completed"
            ? i18n.t("notify.done")
            : data.status === "failed" || data.status === "aborted"
              ? i18n.t("notify.failed")
              : data.status;
        void SendNotification({ id: "turn-end", title: "opencraft", body });
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
      if (key === "n") {
        e.preventDefault();
        void newChat();
      } else if (key === ",") {
        e.preventDefault();
        openConfig();
      } else if (key === "k") {
        e.preventDefault();
        openKanban();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [newChat, openConfig, openKanban]);

  useEffect(() => {
    void Environment().then((env) => setPlatform(env.platform));
  }, []);

  const startDrag =
    () => (e: React.MouseEvent) => {
      e.preventDefault();
      const startX = e.clientX;
      const startW = sidebarW;
      const onMove = (ev: MouseEvent) => {
        const raw = startW + (ev.clientX - startX);
        const next = Math.min(480, Math.max(180, raw));
        setSidebarW(next);
        localStorage.setItem("oc.sidebarW", String(next));
      };
      const onUp = () => {
        window.removeEventListener("mousemove", onMove);
        window.removeEventListener("mouseup", onUp);
      };
      window.addEventListener("mousemove", onMove);
      window.addEventListener("mouseup", onUp);
    };

  if (fatal) {
    return (
      <div className="h-full grid place-items-center">
        <div className="max-w-md rounded-xl border border-err/40 bg-panel p-6 text-sm">
          <h2 className="font-semibold text-err mb-2">
            {t("app.startupFailed")}
          </h2>
          <p className="text-dim whitespace-pre-wrap break-all">
            {fatal || t("app.unknownError")}
          </p>
        </div>
      </div>
    );
  }

  if (!status) {
    return (
      <div className="h-full grid place-items-center text-dim text-sm">
        {t("app.starting")}
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      {platform === "darwin" && (
        <div
          className="shrink-0 flex items-center pl-[78px] pr-4 select-none"
          style={{
            height: 44,
            ["--wails-draggable" as string]: "drag",
          }}
          onDoubleClick={() => WindowToggleMaximise()}
        >
          <span className="text-xs font-semibold text-dim tracking-wide uppercase">
            opencraft
          </span>
        </div>
      )}
      <div className="flex-1 flex min-h-0">
        <div style={{ width: sidebarW }} className="shrink-0">
          <Sidebar />
        </div>
        <div
          onMouseDown={startDrag()}
          className="w-1 shrink-0 cursor-col-resize bg-transparent hover:bg-accent/40"
        />
        <ChatView />
      </div>
      <StatusBar />
      {configOpen && <ConfigPage />}
      {kanbanOpen && <KanbanView />}
    </div>
  );
}
