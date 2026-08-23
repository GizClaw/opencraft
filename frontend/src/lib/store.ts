import { create } from "zustand";
import i18n from "../i18n";
import { api } from "./api";
import type {
  AgentSummary,
  ConfigStatus,
  InteractDTO,
  KanbanCard,
  ReplyRequest,
  SessionMeta,
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

// AssistantItem preserves the stream arrival order of one rendered
// block (reasoning trace, tool call, or text), so the chat renders
// output in the exact order the model produced it instead of
// grouping all reasoning / tools / text together.
export type AssistantItem =
  | { kind: "reasoning"; id: string; text: string }
  | { kind: "tool_call"; id: string; tool: ToolView }
  | { kind: "text"; id: string; text: string };

export interface MessageView {
  id: string;
  role: "user" | "assistant";
  // text carries the user message body; assistant messages render
  // their ordered items instead.
  text: string;
  items: AssistantItem[];
}

let msgSeq = 0;
const newID = (prefix: string) => `${prefix}-${Date.now()}-${msgSeq++}`;

// normalizeArgs coerces the wire form of tool arguments to a string:
// arguments is a json.RawMessage, so the frontend receives a parsed
// object/array rather than text, and rendering it raw crashes React.
function normalizeArgs(args: unknown): string {
  if (typeof args === "string") return args;
  try {
    return JSON.stringify(args, null, 2);
  } catch {
    return String(args);
  }
}

interface StoreState {
  status: ConfigStatus | null;
  configured: boolean;
  fatal: string | null;
  configOpen: boolean;
  workspace: string;
  agents: AgentSummary[];
  sessions: SessionMeta[];
  currentSession: string;
  messages: MessageView[];
  busy: boolean;
  activeRunID: string | null;
  // Multiple prompts can be pending at once (parallel branches or
  // consecutive ask_user calls); each renders as its own card.
  pendingInteracts: InteractDTO[];
  mode: string;
  think: string;
  statusText: string;
  lastUsage: UsageDTO | null;
  kanbanOpen: boolean;
  cards: KanbanCard[];

  init: () => Promise<void>;
  handleEvent: (ev: UIEvent) => void;
  send: (text: string) => Promise<void>;
  replyInteract: (id: string, req: ReplyRequest) => Promise<void>;
  cancelRun: () => Promise<void>;
  openConfig: () => void;
  closeConfig: () => void;
  newChat: () => void;
  resume: (id: string) => Promise<void>;
  deleteSession: (id: string) => Promise<void>;
  setMode: (mode: string) => Promise<void>;
  setThink: (level: string) => Promise<void>;
  refreshAgents: () => Promise<void>;
  loadSessions: () => Promise<void>;
  loadCards: () => Promise<void>;
  openKanban: () => void;
  closeKanban: () => void;
}

export const useStore = create<StoreState>((set, get) => {
  const lastAssistant = (
    messages: MessageView[],
  ): { msg: MessageView; messages: MessageView[] } => {
    const last = messages[messages.length - 1];
    if (!last || last.role !== "assistant") {
    const msg: MessageView = {
      id: newID("msg"),
      role: "assistant",
      text: "",
      items: [],
    };
    return { msg, messages: [...messages, msg] };
  }
    // Always copy the message (and its tools) so every stream delta
    // produces a new array reference; otherwise the zustand selector
    // sees the same reference and React bails the re-render, leaving
    // streamed text and tool updates invisible until another state
    // change happens.
    const msg = { ...last, items: [...last.items] };
    return { msg, messages: [...messages.slice(0, -1), msg] };
  };

  const mergeAppend = (
    msg: MessageView,
    kind: "text" | "reasoning",
    text: string,
  ) => {
    const items = msg.items;
    const lastItem = items[items.length - 1];
    if (lastItem && lastItem.kind === kind) {
      msg.items = [
        ...items.slice(0, -1),
        { ...lastItem, text: lastItem.text + text },
      ];
    } else {
      msg.items = [
        ...items,
        { kind, id: newID("part"), text },
      ];
    }
  };

  const applyStream = (delta: StreamDelta) => {
    if (delta.type !== "part" || !delta.part) return;
    const part = delta.part;
    const { messages } = get();
    switch (part.type) {
      case "text": {
        const { msg, messages: next } = lastAssistant(messages);
        const text = part.text ?? "";
        if (text) mergeAppend(msg, "text", text);
        set({ messages: next });
        break;
      }
      case "reasoning": {
        const { msg, messages: next } = lastAssistant(messages);
        const text = part.text ?? "";
        if (text) mergeAppend(msg, "reasoning", text);
        set({ messages: next });
        break;
      }
      case "tool_call": {
        const { msg, messages: next } = lastAssistant(messages);
        msg.items = [
          ...msg.items,
          {
            kind: "tool_call",
            id: newID("part"),
            tool: {
              id: part.call.id,
              name: part.call.name,
              args: normalizeArgs(part.call.arguments),
              status: "running",
            },
          },
        ];
        set({ messages: next });
        break;
      }
      case "tool_result": {
        const id = part.result.call_id;
        const messages = get().messages;
        let next = messages;
        for (let i = 0; i < messages.length; i++) {
          const m = messages[i];
          if (m.role !== "assistant") continue;
          const items = m.items;
          let changed = false;
          const updatedItems = items.map((item) => {
            if (item.kind !== "tool_call" || item.tool.id !== id) return item;
            changed = true;
            return {
              ...item,
              tool: {
                ...item.tool,
                status: part.result.is_error ? ("error" as const) : ("done" as const),
                result: part.result.content ?? "",
              },
            };
          });
          if (changed) {
            const updatedMsg = { ...m, items: updatedItems };
            next = [
              ...messages.slice(0, i),
              updatedMsg,
              ...messages.slice(i + 1),
            ];
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
    configOpen: false,
    workspace: "",
    agents: [],
    sessions: [],
    currentSession: "",
    messages: [],
    busy: false,
    activeRunID: null,
    pendingInteracts: [],
    mode: "workspace",
    think: "medium",
    statusText: "",
    lastUsage: null,
    kanbanOpen: false,
    cards: [],

    init: async () => {
      const [status, workspace, mode, currentSession, think] =
        await Promise.all([
          api.configStatus(),
          api.workspace(),
          api.sessionMode(),
          api.currentSession(),
          api.getThink(),
        ]);
      set({
        status,
        workspace,
        configured: !status.needed,
        configOpen: status.needed,
        mode,
        think,
        currentSession,
      });
      void get().refreshAgents();
      void get().loadSessions();
    },

    handleEvent: (ev) => {
      switch (ev.type) {
        case "ready":
          set({
            status: ev.data as ConfigStatus,
            configured: true,
            configOpen: false,
            fatal: null,
          });
          void get().refreshAgents();
          break;
        case "onboarding_required":
          set({
            status: ev.data as ConfigStatus,
            configured: false,
            configOpen: true,
          });
          break;
        case "fatal":
          set({ fatal: (ev.data as { error: string }).error ?? "" });
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
          {
            const spec = ev.data as InteractDTO;
            const pending = get().pendingInteracts;
            if (!pending.some((p) => p.id === spec.id)) {
              set({ pendingInteracts: [...pending, spec] });
            }
          }
          break;
        case "resolved": {
          const id = (ev.data as { id: string }).id;
          set({
            pendingInteracts: get().pendingInteracts.filter((p) => p.id !== id),
          });
          break;
        }
        case "turn_end": {
          const data = ev.data as { error?: string; status: string };
          const note =
            data.status === "canceled"
              ? i18n.t("chat.cancelled")
              : data.status === "interrupted"
                ? i18n.t("chat.interrupted")
                : data.status === "failed" || data.status === "aborted"
                  ? i18n.t("chat.failed")
                  : "";
          if (data.error) {
            const { messages } = get();
            const { msg, messages: next } = lastAssistant(messages);
            mergeAppend(msg, "text", `\n\n> ⛔ ${data.error}`);
            set({ messages: next });
          } else if (note) {
            const { messages } = get();
            const { msg, messages: next } = lastAssistant(messages);
            mergeAppend(msg, "text", `\n\n> ⛔ ${note}`);
            set({ messages: next });
          }
          set({ busy: false, activeRunID: null });
          void get().loadSessions();
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
            items: [],
          },
        ],
        busy: true,
        statusText: "",
      });
      try {
        const start = await api.startTurn(trimmed);
        set({ activeRunID: start.run_id, currentSession: start.context_id });
        void get().loadSessions();
      } catch (err) {
        const { messages } = get();
        const { msg, messages: next } = lastAssistant(messages);
        msg.text = `⛔ ${String(err)}`;
        set({ messages: next, busy: false, activeRunID: null });
      }
    },

    replyInteract: async (id, req) => {
      try {
        await api.replyPrompt(id, req);
      } finally {
        set({
          pendingInteracts: get().pendingInteracts.filter((p) => p.id !== id),
        });
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

    openConfig: () => set({ configOpen: true }),
    closeConfig: () => set({ configOpen: false }),

    newChat: async () => {
      try {
        const id = await api.newChat();
        set({ currentSession: id });
      } catch {
        // a fresh conversation id is best-effort; the UI resets anyway
      }
      set({
        messages: [],
        activeRunID: null,
        pendingInteracts: [],
        mode: "workspace",
        think: "medium",
      });
      void get().loadSessions();
    },

    resume: async (id) => {
      try {
        const resumed = await api.resumeSession(id);
        const [mode, history, think] = await Promise.all([
          api.sessionMode(),
          api.sessionHistory(id),
          api.getThink(),
        ]);
        const messages: MessageView[] = history.map((h) =>
          h.role === "user"
            ? {
                id: newID("msg"),
                role: "user",
                text: h.text,
                items: [],
              }
            : {
                id: newID("msg"),
                role: "assistant",
                text: "",
                items: h.text
                  ? [
                      {
                        kind: "text",
                        id: newID("part"),
                        text: h.text,
                      },
                    ]
                  : [],
              },
        );
        set({
          currentSession: resumed,
          mode,
          think,
          messages,
          busy: false,
          activeRunID: null,
          pendingInteracts: [],
        });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setMode: async (mode) => {
      try {
        await api.setSessionMode(mode);
        set({ mode });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setThink: async (level) => {
      try {
        await api.setThink(level);
        set({ think: level });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    deleteSession: async (id) => {
      try {
        await api.deleteSession(id);
        await get().loadSessions();
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

    loadSessions: async () => {
      try {
        set({ sessions: await api.listSessions() });
      } catch {
        // best-effort
      }
    },

    loadCards: async () => {
      try {
        set({ cards: await api.delegationCards() });
      } catch {
        // best-effort
      }
    },

    openKanban: () => set({ kanbanOpen: true }),
    closeKanban: () => set({ kanbanOpen: false }),
  };
});
