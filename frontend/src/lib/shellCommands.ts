import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { chatTargets } from './chatTargets';
import { runningIDsByWorkspace, sessionSlotIDs } from './sessionSlots';
import { useStore } from './store';
import {
  runningConversationIDs,
  useConversationState,
  useFocusState,
} from '../state/react';

export type RunShortcut = (id: string) => void;

const THEME_LABELS = {
  dark: 'config.uiThemeDark',
  light: 'config.uiThemeLight',
  auto: 'config.uiThemeAuto',
} as const;

const THEME_ORDER = ['dark', 'light', 'auto'] as const;

/**
 * useShellCommands maps a shortcut id (lib/keys.ts) to the action it runs.
 *
 * It is the single entry point for all three ways a command can arrive —
 * the keyboard listener, the command palette, and the native macOS menu
 * (whose items bridge back over the `opencraft:menu` event). Anything the
 * shell can be asked to do from more than one of them belongs here, not
 * in a keyboard handler.
 */
export function useShellCommands(): RunShortcut {
  const { t } = useTranslation();
  const focus = useFocusState();
  const sessionID = focus.name === 'active' ? focus.sessionID : '';
  const conversation = useConversationState(sessionID || undefined);
  // Whether a turn is live is the one piece of state the run cannot read
  // from the store at call time, so it is subscribed here. The signature
  // it follows changes on turn transitions, not on every delta.
  const busy =
    conversation?.turn.name === 'starting' ||
    conversation?.turn.name === 'running';

  return useCallback<RunShortcut>(
    (id) => {
      const state = useStore.getState();
      // A command that goes somewhere else closes the surfaces that would
      // otherwise stay on top of what it just opened.
      if (id !== 'palette.open' && state.paletteOpen) state.closePalette();
      if (id !== 'shortcuts.open' && state.shortcutsOpen)
        state.closeShortcuts();

      switch (id) {
        case 'palette.open':
          state.togglePalette();
          return;
        case 'shortcuts.open':
          state.toggleShortcuts();
          return;
        case 'chat.new':
          void state.openDraftChat();
          return;
        case 'settings.open':
          state.openConfig();
          return;
        case 'panel.files':
          togglePanel(state, sessionID, 'files');
          return;
        case 'panel.git':
          togglePanel(state, sessionID, 'git');
          return;
        case 'theme.cycle': {
          const next =
            THEME_ORDER[
              (THEME_ORDER.indexOf(state.theme) + 1) % THEME_ORDER.length
            ];
          state.setTheme(next);
          state.toast(
            t('shortcuts.themeChanged', { theme: t(THEME_LABELS[next]) }),
          );
          return;
        }
        case 'turn.stop':
          if (busy) void state.cancelRun();
          return;
        // Mod+1 … Mod+4 go to the session the sidebar numbers with that
        // digit, in the sidebar's visible order (lib/sessionSlots.ts).
        case 'session.slot1':
        case 'session.slot2':
        case 'session.slot3':
        case 'session.slot4':
          jumpToSessionSlot(
            state,
            Number(id.slice('session.slot'.length)),
            sessionID,
          );
          return;
        case 'session.prev':
          stepSession(state, sessionID, -1);
          return;
        case 'session.next':
          stepSession(state, sessionID, 1);
          return;
        case 'workspace.prev':
          stepWorkspace(state, -1);
          return;
        case 'workspace.next':
          stepWorkspace(state, 1);
          return;
        case 'chat.focusComposer':
          chatTargets()?.focusComposer();
          return;
        case 'chat.copyLastReply':
          chatTargets()?.copyLastReply();
          return;
        case 'chat.prevUser':
          chatTargets()?.walkUserMessage(-1);
          return;
        case 'chat.nextUser':
          chatTargets()?.walkUserMessage(1);
          return;
        default:
          return;
      }
    },
    [busy, sessionID, t],
  );
}

type Store = ReturnType<typeof useStore.getState>;

/** togglePanel opens the chat rail on a segment, or closes it when that
 * segment is already showing — one key, both directions. */
function togglePanel(
  state: Store,
  sessionID: string,
  mode: 'files' | 'git',
): void {
  // The rail belongs to a conversation: a draft chat has nothing to show,
  // and the viewer state is keyed by session id.
  if (sessionID === '') return;
  const viewer = state.viewers[sessionID];
  if ((viewer?.filesOpen ?? false) && (viewer?.panelMode ?? 'files') === mode) {
    state.closeFiles();
    return;
  }
  state.setPanelMode(mode);
  state.openFiles();
}

/** stepSession moves through the workspace's sessions in the order the
 * backend lists them (most recent activity first), which is the order the
 * sidebar shows. The ends are ends: a wrap-around would teleport the user
 * across the list for one keystroke too many. */
function stepSession(state: Store, sessionID: string, delta: 1 | -1): void {
  if (sessionID === '') return;
  const index = state.sessions.findIndex((s) => s.id === sessionID);
  if (index < 0) return;
  const next = state.sessions[index + delta];
  if (next === undefined) return;
  void state.resume(next.id);
}

/** jumpToSessionSlot resumes the session a Mod+digit names: the slot-th row
 * of the active workspace's list, in the order the sidebar draws it — the
 * running conversations first, then the stored rows (lib/sessionSlots.ts,
 * the same call the sidebar numbers its rows with). An empty slot and the
 * session that is already open are no-ops: resume() on the current session
 * closes the surfaces it was asked to re-focus, and a digit must not do
 * that. */
function jumpToSessionSlot(
  state: Store,
  slot: number,
  sessionID: string,
): void {
  if (!Number.isInteger(slot) || slot < 1) return;
  // Read at call time, not from a render: a turn that started since the
  // last render has to lead the list already, or the digit would point at
  // the row below the one the user sees.
  const running =
    runningIDsByWorkspace(runningConversationIDs(), state.pendingPromptConvs)[
      state.workspace
    ] ?? [];
  const target = sessionSlotIDs(state.sessions, running)[slot - 1];
  if (target === undefined || target === sessionID) return;
  void state.resume(target);
}

/** stepWorkspace moves through the workspace history (most recently opened
 * first, the same order the sidebar lists it). */
function stepWorkspace(state: Store, delta: 1 | -1): void {
  const index = state.workspaces.findIndex((w) => w.path === state.workspace);
  if (index < 0) return;
  const next = state.workspaces[index + delta];
  if (next === undefined) return;
  void state.openWorkspace(next.path);
}
