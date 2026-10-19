import type { StoreApi } from 'zustand';
import { api } from '../api';
import { createWorkspaceGlue } from '../conv/workspace';
import { reportFrontendError } from '../frontendErrors';
import { hasUnsentDraft } from '../unsentDraft';
import { sameWorkspacePath } from '../workspacePath';
import { stateRoot } from '../../state/app';
import type { StoreState } from '../store';
import type { WorkspaceMeta } from '../types';

// The workspace list and the switches that follow a click or a draft's
// named target. S4 moved these out of store.ts (move only).

export function createShellWorkspaces(
  store: StoreApi<StoreState>,
  ws: ReturnType<typeof createWorkspaceGlue>,
): Pick<
  StoreState,
  | 'loadWorkspaces'
  | 'chooseWorkspace'
  | 'restoreWorkspaceSession'
  | 'openWorkspace'
  | 'sendFirstMessage'
  | 'removeWorkspace'
> {
  const set = store.setState;
  const get = store.getState;
  const { switchWorkspace } = ws;

  // keepForComposer says the page holds a draft this switch would not
  // clear. The flag comes from the composer (lib/unsentDraft), so it
  // answers "there is text or an attachment in the composer" without
  // saying whose it is: a draft typed inside a session is emptied by the
  // switch - the reset below lands on no-session and ChatView clears the
  // composer on the way in - and only a draft of a conversation that has
  // not started survives the reset. The focus is still the one of the
  // workspace being left, so it is what separates the two; asking after
  // the reset committed would make the answer depend on whether the ready
  // event beat the binding's return.
  function keepForComposer(): boolean {
    return hasUnsentDraft() && stateRoot.focusSnapshot.value === 'no-session';
  }

  // sameWorkspaceList reports whether a refreshed history equals the one
  // already in the store. Refreshes fire on every ready event and after
  // every switch, and an unchanged list must not replace the array: the
  // sidebar re-flattens its history tree off it.
  function sameWorkspaceList(
    prev: WorkspaceMeta[],
    next: WorkspaceMeta[],
  ): boolean {
    if (prev === next) return true;
    if (prev.length !== next.length) return false;
    return prev.every((w, i) => {
      const other = next[i];
      return (
        w.id === other.id &&
        w.path === other.path &&
        w.title === other.title &&
        w.last_opened === other.last_opened
      );
    });
  }

  let workspaceSwitchSeq = 0;

  // Workspace history refreshes race each other: a switch triggers one
  // reload from the "ready" event (before the backend has stamped
  // last_opened) and one from the caller once the binding returned.
  // Only the newest request may apply its snapshot.
  let workspacesLoadSeq = 0;

  return {
    loadWorkspaces: async () => {
      const seq = (workspacesLoadSeq += 1);
      try {
        const next = (await api.workspaces()) ?? [];
        if (seq !== workspacesLoadSeq) return;
        if (sameWorkspaceList(get().workspaces, next)) return;
        set({ workspaces: next });
      } catch {
        // best-effort
      }
    },

    chooseWorkspace: async () => {
      try {
        const path = (await api.chooseWorkspace()) ?? '';
        if (path) await get().openWorkspace(path);
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    restoreWorkspaceSession: async (workDir) => {
      const seq = ++workspaceSwitchSeq;
      stateRoot.sendFocus({ type: 'WORKSPACE_RESET' });
      if (!workDir || get().workspace !== workDir) return;
      let currentSession = '';
      try {
        currentSession = await api.currentSession();
      } catch {
        // Fall through and mint a fresh session below.
      }
      if (seq !== workspaceSwitchSeq || get().workspace !== workDir) {
        return;
      }
      if (!currentSession) {
        await get().newChat();
        return;
      }
      await get().resume(currentSession);
      const focus = stateRoot.focusSnapshot;
      if (
        seq === workspaceSwitchSeq &&
        get().workspace === workDir &&
        focus.value !== 'active'
      ) {
        // The saved session is gone or cannot be resumed; land on a
        // fresh conversation instead of leaving the workspace blank.
        await get().newChat();
      }
    },

    openWorkspace: async (path) => {
      // A switch to the workspace the window is already on is not a
      // switch: it would rebuild the runtime behind the reader and, if it
      // reached the reload below, throw the page away for nothing.
      //
      // The window's own belief is not enough to decide that: it switches
      // on what the ready event carries, so one that never arrived leaves
      // it believing the wrong workspace - and re-picking the workspace it
      // believes it is on used to be the way out of that state (the call
      // re-opened the backend's actual workspace and the next ready pulled
      // the window back in line). The backend's answer is one getter, not
      // a rebuild, so the early return holds when both agree - and an
      // unanswerable one counts as agreement: the repair waits for an
      // answer rather than rebuilding a runtime on a guess.
      if (sameWorkspacePath(path, get().workspace)) {
        const active = await api.workspace().catch(() => '');
        if (!active || sameWorkspacePath(active, path)) return;
      }
      try {
        await api.openWorkspace(path);
        // The runtime rebuild emits "ready"; the ready handler
        // restores the target workspace's session and refreshes
        // sessions. Its workspace refresh is not enough to rely on:
        // it runs before the backend stamped last_opened (see
        // Workspace.Open), so read the history again here.
        void get().loadWorkspaces();
        // The switch is also the window's memory boundary. The page this
        // one replaces was holding a transcript the reader has left: its
        // decoded images, its WebKit text objects, and a high-water mark
        // the window only gives back by going away. The binding has
        // returned, so the backend is on the new workspace and a fresh
        // page boots straight onto it, and none of the switch has been
        // painted yet.
        //
        // This is the only path that reloads, and deliberately so: the
        // two switches that carry state with them - a draft sent to
        // another workspace (sendFirstMessage) and a session opened in
        // one (openSessionInWorkspace) - would lose the operation a
        // reload restarts the page out from under. A workspace the
        // backend leaves on its own (the reader deleted the one they were
        // in) lands through applyWorkspaceSwitch without a reload either.
        // So the boundary is reached on the ordinary switch, not on all
        // of them.
        //
        // Two things keep the page, both expected rather than rare: a
        // live run anywhere in the process (its deltas are addressed to
        // this window; the Go side logs the refusal with the workspace
        // list), and a composer holding work that only this document has
        // (see unsentDraft, which keeps the call from being made, so this
        // one leaves no Go log line - the composer is the visible
        // reason). The second is the draft of a conversation that has not
        // started yet, and the flag alone cannot tell it from one typed
        // inside a session: the switch itself clears that one
        // (WORKSPACE_RESET puts the focus back to no-session, ChatView
        // empties the composer on the way in), and the flag learns about
        // it a commit later - which is why the focus is read beside it
        // rather than waiting for the clearing to land. The one thing a
        // switch can still drop is a settings edit that has not reached
        // disk: SettingsGeneral debounces its slider and flushes on
        // unmount, and this navigation may replace the page first.
        // Neither half is retried here - the next switch asks again.
        if (!keepForComposer()) {
          void api
            .reloadWindow()
            .catch((err) => reportFrontendError('reload-window', err));
        }
      } catch (err) {
        set({ statusText: String(err) });
      }
    },

    sendFirstMessage: async (
      workspacePath,
      text,
      attachments = [],
      options,
    ) => {
      const trimmed = text.trim();
      if ((!trimmed && attachments.length === 0) || !get().configured) {
        return false;
      }
      const current = get().workspace;
      const target =
        workspacePath && workspacePath !== current ? workspacePath : current;
      if (!target) return false;
      if (target !== current) {
        ws.suppressRestore(target);
        try {
          await api.openWorkspace(target);
          const landed = await switchWorkspace(target, 8000);
          // A draft is a send to a named workspace, so a switch that did
          // not land — nothing settled, or a newer switch took the window
          // first — is reported rather than dropped the way a session
          // click is: the text goes back to the composer and the status
          // line says why it did not go.
          if (!landed || !sameWorkspacePath(landed, target)) {
            throw new Error('workspace switch did not complete');
          }
          void get().loadWorkspaces();
        } catch (err) {
          ws.suppressRestore(null);
          set({ statusText: String(err) });
          return false;
        }
        ws.suppressRestore(null);
      }
      // The draft becomes a real conversation now: mint it in the
      // selected workspace and send the staged first message.
      await get().newChat();
      if (stateRoot.focusSnapshot.value !== 'active') {
        return false;
      }
      // Draft pre-send choices land on the fresh session before its
      // first run starts. Without explicit options the minted
      // conversation already carries the configured session defaults.
      if (options) {
        if (options.mode) {
          await get().setMode(options.mode);
        }
        if (options.think) {
          await get().setThink(options.think);
        }
        await get().setModel(options.model ?? '');
      }
      await get().send(trimmed, attachments);
      return true;
    },

    removeWorkspace: async (id) => {
      try {
        await api.removeWorkspace(id);
        await get().loadWorkspaces();
      } catch (err) {
        set({ statusText: String(err) });
      }
    },
  };
}
