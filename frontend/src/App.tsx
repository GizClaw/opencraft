import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { EventsOn } from "../wailsjs/runtime/runtime";
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
  const { t } = useTranslation();

  useEffect(() => {
    void init();
    const off = EventsOn("opencraft:ui", (ev: UIEvent) => handleEvent(ev));
    return off;
  }, [init, handleEvent]);

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
        <Sidebar />
        <ChatView />
        <FilePanel />
      </div>
      <StatusBar />
      {configOpen && <ConfigPage />}
      {kanbanOpen && <KanbanView />}
    </div>
  );
}
