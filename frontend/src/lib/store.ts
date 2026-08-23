import { create } from "zustand";
import { api } from "./api";
import type {
  AgentSummary,
  ConfigStatus,
  InteractDTO,
  ReplyRequest,
  StreamDelta,
  UIEvent,
  UsageDTO,
} from "./types";

export interface ToolView {
  id: string;
  name: string;
  args: string;
  status: "running" | "done" | "error";
  result?: string;
}

export interface MessageView {
  id: string;
  role: "user" | "assistant";
  text: string;
  reasoning: string;
  tools: ToolView[];
}

let msgSeq = 0;
const newID = (prefix: string) => `${prefix}-${Date.now()}-${msgSeq++}`;

interface StoreState {
  status: ConfigStatus | null;
  configured: boolean;
  fatal: string | null;
  onboardingOpen: boolean;
  workspace: string;
  agents: AgentSummary[];
  messages: MessageView[];
  busy: boolean;
  activeRunID: string | null;
  pendingInteract: InteractDTO | null;
  mode: string;
  statusText: string;
  lastUsage: UsageDTO | null;

  init: () => Promise<void>;
  handleEvent: (ev: UIEvent) => void;
  send: (text: string) => Promise<void>;
  replyInteract: (req: ReplyRequest) => Promise<void>;
  cancelRun: () => Promise<void>;
  openOnboarding: () => void;
  closeOnboarding: () => void;
  newChat: () => void;
  setMode: (mode: string) => Promise<void>;
  refreshAgents: () => Promise<void>;
}

export const useStore = create<StoreState>((set, get) => {
  const lastAssistant = (
    messages: MessageView[],
  ): { msg: MessageView; messages: MessageView[] } => {
    let msg = messages[messages.length - 1];
    if (!msg || msg.role !== "assistant") {
      msg = {
        id: newID("msg"),
        role: "assistant",
        text: "",
        reasoning: "",
        tools: [],
      };
      messages = [...messages, msg];
    }
    return { msg, messages };
  };

  const applyStream = (delta: StreamDelta) => {
    if (delta.type !== "part" || !delta.part) return;
    const part = delta.part;
    const { messages } = get();
    switch (part.type) {
      case "text": {
        const { msg, messages: next } = lastAssistant(messages);
        msg.text += part.text ?? "";
        set({ messages: next });
        break;
      }
      case "reasoning": {
        const { msg, messages: next } = lastAssistant(messages);
        msg.reasoning += part.text ?? "";
        set({ messages: next });
        break;
      }
      case "tool_call": {
        const { msg, messages: next } = lastAssistant(messages);
        msg.tools.push({
          id: part.call.id,
          name: part.call.name,
          args: part.call.arguments ?? "",
          status: "running",
        });
        set({ messages: next });
        break;
      }
      case "tool_result": {
        const id = part.result.call_id;
        const { messages: next } = get();
        for (const m of next) {
          const tool = m.tools.find((t) => t.id === id);
          if (tool) {
            tool.status = part.result.is_error ? "error" : "done";
            tool.result = part.result.content ?? "";
            break;
          }
        }
        set({ messages: next });
        break;
      }
    }
  };

  return {
    status: null,
    configured: false,
    fatal: null,
    onboardingOpen: false,
    workspace: "",
    agents: [],
    messages: [],
    busy: false,
    activeRunID: null,
    pendingInteract: null,
    mode: "workspace",
    statusText: "",
    lastUsage: null,

    init: async () => {
      const [status, workspace, mode] = await Promise.all([
        api.configStatus(),
        api.workspace(),
        api.sessionMode(),
      ]);
      set({
        status,
        workspace,
        configured: !status.needed,
        onboardingOpen: status.needed,
        mode,
      });
      void get().refreshAgents();
    },

    handleEvent: (ev) => {
      switch (ev.type) {
        case "ready":
          set({
            status: ev.data as ConfigStatus,
            configured: true,
            onboardingOpen: false,
            fatal: null,
          });
          void get().refreshAgents();
          break;
        case "onboarding_required":
          set({
            status: ev.data as ConfigStatus,
            configured: false,
            onboardingOpen: true,
          });
          break;
        case "fatal":
          set({ fatal: (ev.data as { error: string }).error ?? "未知错误" });
          break;
        case "stream":
          applyStream(ev.data as StreamDelta);
          break;
        case "status":
          set({ statusText: (ev.data as { text: string }).text });
          break;
        case "usage":
          set({ lastUsage: ev.data as UsageDTO });
          break;
        case "interact":
          set({ pendingInteract: ev.data as InteractDTO });
          break;
        case "resolved": {
          const id = (ev.data as { id: string }).id;
          if (get().pendingInteract?.id === id) {
            set({ pendingInteract: null });
          }
          break;
        }
        case "turn_end": {
          const data = ev.data as { error?: string; status: string };
          if (data.error) {
            const { messages } = get();
            const { msg, messages: next } = lastAssistant(messages);
            msg.text +=
              msg.text.length > 0 ? `\n\n> ⛔ ${data.error}` : `⛔ ${data.error}`;
            set({ messages: next });
          }
          set({ busy: false, activeRunID: null });
          break;
        }
      }
    },

    send: async (text) => {
      const trimmed = text.trim();
      if (!trimmed || get().busy || !get().configured) return;
      set({
        messages: [
          ...get().messages,
          {
            id: newID("msg"),
            role: "user",
            text: trimmed,
            reasoning: "",
            tools: [],
          },
        ],
        busy: true,
        statusText: "等待回复…",
      });
      try {
        const start = await api.startTurn(trimmed);
        set({ activeRunID: start.run_id });
      } catch (err) {
        const { messages } = get();
        const { msg, messages: next } = lastAssistant(messages);
        msg.text = `⛔ ${String(err)}`;
        set({ messages: next, busy: false, activeRunID: null });
      }
    },

    replyInteract: async (req) => {
      const pending = get().pendingInteract;
      if (!pending) return;
      try {
        await api.replyPrompt(pending.id, req);
      } finally {
        set({ pendingInteract: null });
      }
    },

    cancelRun: async () => {
      const runID = get().activeRunID;
      if (runID) {
        try {
          await api.cancelTurn(runID);
        } catch {
          // turn_end will settle the UI regardless
        }
      }
    },

    openOnboarding: () => set({ onboardingOpen: true }),
    closeOnboarding: () => set({ onboardingOpen: false }),

    newChat: async () => {
      try {
        await api.newChat();
      } catch {
        // a fresh conversation id is best-effort; the UI resets anyway
      }
      set({
        messages: [],
        activeRunID: null,
        pendingInteract: null,
        mode: "workspace",
      });
    },

    setMode: async (mode) => {
      try {
        await api.setSessionMode(mode);
        set({ mode });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    refreshAgents: async () => {
      try {
        set({ agents: await api.listAgents() });
      } catch {
        // registry read is best-effort
      }
    },
  };
});
