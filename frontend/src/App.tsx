import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "./i18n";
import {
  EventsOn,
  InitializeNotifications,
  RequestNotificationAuthorization,
  SendNotification,
} from "../wailsjs/runtime/runtime";
import { ChatView } from "./components/ChatView";
import { ConfigPage } from "./components/ConfigPage";
import { FilePanel } from "./components/FilePanel";
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
  const [sidebarW, setSidebarW] = useState(
    () => Number(localStorage.getItem("oc.sidebarW")) || 240,
  );
  const [fileW, setFileW] = useState(
    () => Number(localStorage.getItem("oc.fileW")) || 320,
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

  const startDrag =
    (side: "sidebar" | "file") => (e: React.MouseEvent) => {
      e.preventDefault();
      const startX = e.clientX;
      const startW = side === "sidebar" ? sidebarW : fileW;
      const onMove = (ev: MouseEvent) => {
        const raw =
          side === "sidebar"
            ? startW + (ev.clientX - startX)
            : startW + (startX - ev.clientX);
        const next = Math.min(480, Math.max(180, raw));
        if (side === "sidebar") {
          setSidebarW(next);
          localStorage.setItem("oc.sidebarW", String(next));
        } else {
          setFileW(next);
          localStorage.setItem("oc.fileW", String(next));
        }
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
      <div className="flex-1 flex min-h-0">
        <div style={{ width: sidebarW }} className="shrink-0">
          <Sidebar />
        </div>
        <div
          onMouseDown={startDrag("sidebar")}
          className="w-1 shrink-0 cursor-col-resize bg-transparent hover:bg-accent/40"
        />
        <ChatView />
        <div
          onMouseDown={startDrag("file")}
          className="w-1 shrink-0 cursor-col-resize bg-transparent hover:bg-accent/40"
        />
        <div style={{ width: fileW }} className="shrink-0">
          <FilePanel />
        </div>
      </div>
      <StatusBar />
      {configOpen && <ConfigPage />}
      {kanbanOpen && <KanbanView />}
    </div>
  );
}
