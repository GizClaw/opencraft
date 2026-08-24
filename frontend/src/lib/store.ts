import { create } from "zustand";
import i18n from "../i18n";
import { api } from "./api";
import type {
  AgentSummary,
  ConfigStatus,
  InteractDTO,
  KanbanCard,
  HistoryMessage,
  ModelOption,
  ReplyRequest,
  SessionMeta,
  StreamDelta,
  UIEvent,
  UsageDTO,
  WorkspaceMeta,
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

// ConversationState is the live UI state of one conversation. Each
// conversation owns its transcript, turn state, permission mode,
// think level, and pending prompts, so turns in different
// conversations can run in parallel.
export interface ConversationState {
  messages: MessageView[];
  busy: boolean;
  activeRunID: string | null;
  mode: string;
  think: string;
  model: string;
  pendingInteracts: InteractDTO[];
  lastFailed: boolean;
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

const emptyConv = (): ConversationState => ({
  messages: [],
  busy: false,
  activeRunID: null,
  mode: "workspace",
  think: "medium",
  model: "",
  pendingInteracts: [],
  lastFailed: false,
});

// historyToMessages converts stored flowcraft messages back into the
// live MessageView shape: user text, then assistant messages with the
// same ordered blocks (reasoning, tool calls, text) the stream
// produces; tool results are matched back to their tool call.
const historyToMessages = (history: HistoryMessage[]): MessageView[] => {
  const messages: MessageView[] = [];
  const toolCalls: {
    item: Extract<AssistantItem, { kind: "tool_call" }>;
  }[] = [];
  for (const h of history) {
    const parts = h.content?.parts ?? [];
    if (h.role === "user") {
      const text = parts
        .filter((p): p is { type: "text"; text?: string } => p.type === "text")
        .map((p) => p.text ?? "")
        .join("");
      messages.push({ id: newID("msg"), role: "user", text, items: [] });
      continue;
    }
    if (h.role === "tool") {
      for (const p of parts) {
        if (p.type !== "tool_result" || !p.result) continue;
        const call = toolCalls.find(
          (c) => c.item.tool.id === p.result!.call_id,
        );
        if (call) {
          call.item.tool.status = p.result.is_error ? "error" : "done";
          call.item.tool.result = p.result.content;
        }
      }
      continue;
    }
    const msg: MessageView = {
      id: newID("msg"),
      role: "assistant",
      text: "",
      items: [],
    };
    for (const p of parts) {
      switch (p.type) {
        case "text":
          if (p.text) {
            msg.items.push({ kind: "text", id: newID("part"), text: p.text });
          }
          break;
        case "reasoning":
          if (p.text) {
            msg.items.push({
              kind: "reasoning",
              id: newID("part"),
              text: p.text,
            });
          }
          break;
        case "tool_call": {
          const call = p.call;
          if (!call) break;
          const item: Extract<AssistantItem, { kind: "tool_call" }> = {
            kind: "tool_call",
            id: newID("part"),
            tool: {
              id: call.id,
              name: call.name,
              args: normalizeArgs(call.arguments),
              status: "running",
            },
          };
          msg.items.push(item);
          toolCalls.push({ item });
          break;
        }
      }
    }
    messages.push(msg);
  }
  return messages;
};

// lastAssistant returns a mutable copy of the last assistant message
// (creating one when needed) plus a NEW messages array, so every
// stream delta produces fresh references and React re-renders.
function lastAssistant(
  messages: MessageView[],
): { msg: MessageView; messages: MessageView[] } {
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
  const msg = { ...last, items: [...last.items] };
  return { msg, messages: [...messages.slice(0, -1), msg] };
}

function mergeAppend(
  msg: MessageView,
  kind: "text" | "reasoning",
  text: string,
) {
  const items = msg.items;
  const lastItem = items[items.length - 1];
  if (lastItem && lastItem.kind === kind) {
    msg.items = [
      ...items.slice(0, -1),
      { ...lastItem, text: lastItem.text + text },
    ];
  } else {
    msg.items = [...items, { kind, id: newID("part"), text }];
  }
}

// applyStream folds one stream delta into a message list and returns
// the new list (immutable).
function applyStream(messages: MessageView[], delta: StreamDelta): MessageView[] {
  if (delta.type !== "part" || !delta.part) return messages;
  const part = delta.part;
  switch (part.type) {
    case "text": {
      const text = part.text ?? "";
      if (!text) return messages;
      const { msg, messages: next } = lastAssistant(messages);
      mergeAppend(msg, "text", text);
      return next;
    }
    case "reasoning": {
      const text = part.text ?? "";
      if (!text) return messages;
      const { msg, messages: next } = lastAssistant(messages);
      mergeAppend(msg, "reasoning", text);
      return next;
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
      return next;
    }
    case "tool_result": {
      const id = part.result.call_id;
      let next = messages;
      for (let i = 0; i < messages.length; i++) {
        const m = messages[i];
        if (m.role !== "assistant") continue;
        let changed = false;
        const updatedItems = m.items.map((item) => {
          if (item.kind !== "tool_call" || item.tool.id !== id) return item;
          changed = true;
          return {
            ...item,
            tool: {
              ...item.tool,
              status: part.result.is_error
                ? ("error" as const)
                : ("done" as const),
              result: part.result.content ?? "",
            },
          };
        });
        if (changed) {
          next = [
            ...messages.slice(0, i),
            { ...m, items: updatedItems },
            ...messages.slice(i + 1),
          ];
          break;
        }
      }
      return next;
    }
    default:
      return messages;
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
  current: string;
  conversations: Record<string, ConversationState>;
  runConvs: Record<string, string>;
  statusText: string;
  lastUsage: UsageDTO | null;
  kanbanOpen: boolean;
  cards: KanbanCard[];
  modelOptions: ModelOption[];
  theme: "dark" | "light";
  workspaces: WorkspaceMeta[];

  init: () => Promise<void>;
  handleEvent: (ev: UIEvent) => void;
  send: (text: string) => Promise<void>;
  retryLast: () => Promise<void>;
  replyInteract: (id: string, req: ReplyRequest) => Promise<void>;
  cancelRun: () => Promise<void>;
  openConfig: () => void;
  closeConfig: () => void;
  newChat: () => Promise<void>;
  resume: (id: string) => Promise<void>;
  deleteSession: (id: string) => Promise<void>;
  setMode: (mode: string) => Promise<void>;
  setThink: (level: string) => Promise<void>;
  setModel: (model: string) => Promise<void>;
  setTheme: (theme: "dark" | "light") => void;
  loadWorkspaces: () => Promise<void>;
  openWorkspace: (path: string) => Promise<void>;
  removeWorkspace: (id: string) => Promise<void>;
  refreshAgents: () => Promise<void>;
  loadSessions: () => Promise<void>;
  loadCards: () => Promise<void>;
  openKanban: () => void;
  closeKanban: () => void;
  flash: (text: string) => void;
}

export const useStore = create<StoreState>((set, get) => {
  const updateConv = (id: string, patch: Partial<ConversationState>) =>
    set((state) => {
      const conv = state.conversations[id];
      if (!conv) return state;
      return {
        conversations: {
          ...state.conversations,
          [id]: { ...conv, ...patch },
        },
      };
    });

  const beginTurn = async (
    convID: string,
    text: string,
    messages: MessageView[],
  ) => {
    updateConv(convID, { messages, busy: true, lastFailed: false });
    try {
      const start = await api.startTurn(text);
      set((state) => ({
        runConvs: { ...state.runConvs, [start.run_id]: convID },
        conversations: {
          ...state.conversations,
          [convID]: {
            ...state.conversations[convID],
            activeRunID: start.run_id,
          },
        },
      }));
      void get().loadSessions();
    } catch (err) {
      const conv = get().conversations[convID];
      if (conv) {
        const { msg, messages: next } = lastAssistant(conv.messages);
        mergeAppend(msg, "text", `⛔ ${String(err)}`);
        updateConv(convID, { messages: next, busy: false });
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
    current: "",
    conversations: {},
    runConvs: {},
    statusText: "",
    lastUsage: null,
    kanbanOpen: false,
    cards: [],
    modelOptions: [],
    theme: "dark",
    workspaces: [],

    init: async () => {
      const saved = window.localStorage.getItem("opencraft.theme");
      const theme = saved === "light" ? "light" : "dark";
      document.documentElement.classList.toggle(
        "theme-light",
        theme === "light",
      );
      const [status, workspace, mode, currentSession, think, model, modelOptions] =
        await Promise.all([
          api.configStatus(),
          api.workspace(),
          api.sessionMode(),
          api.currentSession(),
          api.getThink(),
          api.getModel(),
          api.modelOptions(),
        ]);
      set({
        status,
        workspace,
        configured: !status.needed,
        configOpen: false,
        current: currentSession,
        conversations: {
          [currentSession]: { ...emptyConv(), mode, think, model },
        },
        modelOptions,
        theme,
      });
      void get().refreshAgents();
      void get().loadWorkspaces();
      void get().loadSessions();
    },

    handleEvent: (ev) => {
      switch (ev.type) {
        case "ready": {
          const data = ev.data as ConfigStatus;
          if (data.work_dir !== get().workspace) {
            // Workspace switched: start fresh in the new workspace.
            set({
              workspace: data.work_dir,
              conversations: {
                [get().current]: emptyConv(),
              },
              runConvs: {},
            });
            void get().loadSessions();
          }
          set({
            status: data,
            configured: true,
            configOpen: false,
            fatal: null,
          });
          void api.modelOptions().then((modelOptions) =>
            set({ modelOptions }),
          );
          void get().refreshAgents();
          void get().loadWorkspaces();
          break;
        }
        case "fatal":
          set({ fatal: (ev.data as { error: string }).error ?? "" });
          break;
        case "stream": {
          const data = ev.data as { run_id?: string; delta: StreamDelta };
          const convID =
            (data.run_id && get().runConvs[data.run_id]) || get().current;
          const conv = get().conversations[convID];
          if (!conv) break;
          updateConv(convID, {
            messages: applyStream(conv.messages, data.delta),
          });
          break;
        }
        case "status":
          set({ statusText: (ev.data as { text: string }).text });
          break;
        case "usage":
          set({ lastUsage: ev.data as UsageDTO });
          break;
        case "interact": {
          const spec = ev.data as InteractDTO;
          const convID =
            (spec.run_id && get().runConvs[spec.run_id]) || get().current;
          const conv = get().conversations[convID];
          if (!conv) break;
          if (!conv.pendingInteracts.some((p) => p.id === spec.id)) {
            updateConv(convID, {
              pendingInteracts: [...conv.pendingInteracts, spec],
            });
          }
          break;
        }
        case "resolved": {
          const id = (ev.data as { id: string }).id;
          for (const [convID, conv] of Object.entries(get().conversations)) {
            if (conv.pendingInteracts.some((p) => p.id === id)) {
              updateConv(convID, {
                pendingInteracts: conv.pendingInteracts.filter(
                  (p) => p.id !== id,
                ),
              });
              break;
            }
          }
          break;
        }
        case "turn_end": {
          const data = ev.data as {
            run_id?: string;
            status: string;
            error?: string;
          };
          const convID =
            (data.run_id && get().runConvs[data.run_id]) || get().current;
          const conv = get().conversations[convID];
          if (!conv) break;
          const failed =
            data.status === "failed" ||
            data.status === "aborted" ||
            data.status === "canceled" ||
            data.status === "interrupted";
          let messages = conv.messages;
          const note = failed
            ? data.status === "canceled"
              ? i18n.t("chat.cancelled")
              : data.status === "interrupted"
                ? i18n.t("chat.interrupted")
                : i18n.t("chat.failed")
            : "";
          if (data.error || (failed && note)) {
            const { msg, messages: next } = lastAssistant(messages);
            mergeAppend(msg, "text", `\n\n> ⛔ ${data.error || note}`);
            messages = next;
          }
          set((state) => {
            const runConvs = { ...state.runConvs };
            delete runConvs[data.run_id ?? ""];
            return {
              runConvs,
              conversations: {
                ...state.conversations,
                [convID]: {
                  ...state.conversations[convID],
                  messages,
                  busy: false,
                  activeRunID: null,
                  lastFailed: failed,
                },
              },
            };
          });
          void get().loadSessions();
          break;
        }
        case "session_updated":
          void get().loadSessions();
          break;
      }
    },

    send: async (text) => {
      const trimmed = text.trim();
      const state = get();
      const conv = state.conversations[state.current];
      if (!trimmed || !conv || conv.busy || !state.configured) return;
      const messages = [
        ...conv.messages,
        {
          id: newID("msg"),
          role: "user" as const,
          text: trimmed,
          items: [],
        },
      ];
      await beginTurn(state.current, trimmed, messages);
    },

    retryLast: async () => {
      const state = get();
      const conv = state.conversations[state.current];
      if (!conv || conv.busy) return;
      let lastUserIdx = -1;
      for (let i = conv.messages.length - 1; i >= 0; i--) {
        if (conv.messages[i].role === "user") {
          lastUserIdx = i;
          break;
        }
      }
      if (lastUserIdx < 0) return;
      const text = conv.messages[lastUserIdx].text;
      await beginTurn(
        state.current,
        text,
        conv.messages.slice(0, lastUserIdx + 1),
      );
    },

    replyInteract: async (id, req) => {
      try {
        await api.replyPrompt(id, req);
      } finally {
        for (const [convID, conv] of Object.entries(get().conversations)) {
          if (conv.pendingInteracts.some((p) => p.id === id)) {
            updateConv(convID, {
              pendingInteracts: conv.pendingInteracts.filter(
                (p) => p.id !== id,
              ),
            });
            break;
          }
        }
      }
    },

    cancelRun: async () => {
      const conv = get().conversations[get().current];
      if (conv?.activeRunID) {
        try {
          await api.cancelTurn(conv.activeRunID);
        } catch {
          // turn_end settles the UI regardless
        }
      }
    },

    openConfig: () => set({ configOpen: true }),
    closeConfig: () => set({ configOpen: false }),

    newChat: async () => {
      try {
        const id = await api.newChat();
        set((state) => ({
          current: id,
          conversations: {
            ...state.conversations,
            [id]: emptyConv(),
          },
        }));
      } catch {
        // best-effort
      }
      void get().loadSessions();
    },

    resume: async (id) => {
      try {
        // Switch the backend session context first (conversation id,
        // mode, think, model all follow the selected session); without
        // this the mode/think/model reads below return the previous
        // conversation's values and new turns land in the old session.
        await api.resumeSession(id);
        if (get().conversations[id]) {
          set({ current: id });
          return;
        }
        const [mode, history, think, model] = await Promise.all([
          api.sessionMode(),
          api.sessionHistory(id),
          api.getThink(),
          api.getModel(),
        ]);
        set((state) => ({
          current: id,
          conversations: {
            ...state.conversations,
            [id]: {
              ...emptyConv(),
              mode,
              think,
              model,
              messages: historyToMessages(history),
            },
          },
        }));
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    deleteSession: async (id) => {
      try {
        await api.deleteSession(id);
        set((state) => {
          const conversations = { ...state.conversations };
          delete conversations[id];
          return { conversations };
        });
        await get().loadSessions();
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setMode: async (mode) => {
      try {
        await api.setSessionMode(mode);
        updateConv(get().current, { mode });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setThink: async (level) => {
      try {
        await api.setThink(level);
        updateConv(get().current, { think: level });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setModel: async (model) => {
      try {
        await api.setModel(model);
        updateConv(get().current, { model });
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    setTheme: (theme) => {
      document.documentElement.classList.toggle(
        "theme-light",
        theme === "light",
      );
      window.localStorage.setItem("opencraft.theme", theme);
      set({ theme });
    },

    refreshAgents: async () => {
      try {
        set({ agents: await api.listAgents() });
      } catch {
        // best-effort
      }
    },

    loadSessions: async () => {
      try {
        set({ sessions: await api.listSessions() });
      } catch {
        // best-effort
      }
    },

    loadWorkspaces: async () => {
      try {
        set({ workspaces: await api.workspaces() });
      } catch {
        // best-effort
      }
    },

    openWorkspace: async (path) => {
      try {
        await api.openWorkspace(path);
        // The runtime rebuild emits "ready", which resets the
        // conversations and refreshes sessions/workspaces.
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    removeWorkspace: async (id) => {
      try {
        await api.removeWorkspace(id);
        await get().loadWorkspaces();
      } catch (err) {
        set({ statusText: String(err) });
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
    flash: (text) => set({ statusText: text }),
  };
});
