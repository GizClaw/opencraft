import type { StoreApi } from 'zustand';
import i18n from '../../i18n';
import { INITIAL_HISTORY_TURNS, historyPage } from '../conv/history';
import { activeConversationID } from '../conv/core';
import { api } from '../api';
import {
  carryUnarchivedSteers,
  emptyConv,
  errorMessage,
  settleConversation,
  viewerDefaults,
} from '../conv/model';
import { createConvCore } from '../conv/core';
import { createConvPaging } from '../conv/paging';
import { createWorkspaceGlue } from '../conv/workspace';
import { liveTurnEntry } from '../conv/artifacts';
import { measureInteraction } from '../perfMetrics';
import { sameWorkspacePath } from '../workspacePath';
import { stateRoot } from '../../state/app';
import type { FileTab } from '../types';
import type { StoreState } from '../store';

// Sessions: minting, resuming, deleting, hydrating the list and the
// draft flows around a first message. S4 moved these out of store.ts
// (move only).

export function createShellSessions(
  store: StoreApi<StoreState>,
  core: ReturnType<typeof createConvCore>,
  paging: ReturnType<typeof createConvPaging>,
  ws: ReturnType<typeof createWorkspaceGlue>,
): Pick<
  StoreState,
  | 'newEmptyTab'
  | 'newChat'
  | 'openDraftChat'
  | 'backFromFailure'
  | 'resume'
  | 'deleteSession'
  | 'loadSessions'
  | 'openSessionInWorkspace'
  | 'openFromNotification'
> {
  const set = store.setState;
  const get = store.getState;
  const {
    clearPendingIndex,
    flushPendingStreams,
    openMintedSession,
    retainLiveConversations,
    runContextSwitch,
    syncPendingIndex,
  } = core;
  const { switchWorkspace } = ws;
  const { waitForWorkspaceRestore } = ws;

  // clearTurnAttention drops the sidebar's failure marker for one
  // conversation: opening the row is the reader answering it, and a
  // delete takes the row away for good.
  const clearTurnAttention = (id: string) => {
    if (!get().turnAttention[id]) return;
    set((state) => {
      const next = { ...state.turnAttention };
      delete next[id];
      return { turnAttention: next };
    });
  };

  return {
    newEmptyTab: () => {
      const id = activeConversationID();
      if (!id) return;
      set((state) => {
        const viewer = state.viewers[id] ?? viewerDefaults();
        const key = `untitled-${Date.now()}-${Math.random()
          .toString(36)
          .slice(2, 7)}`;
        const tab: FileTab = {
          key,
          path: '',
          rel: '',
          root: 'workspace',
          name: i18n.t('files.newFile'),
          media_type: '',
        };
        return {
          viewers: {
            ...state.viewers,
            [id]: {
              ...viewer,
              filesOpen: true,
              fileTabs: [...viewer.fileTabs, tab],
              fileActive: key,
            },
          },
        };
      });
    },

    newChat: async () => {
      stateRoot.sendFocus({ type: 'OPEN_NEW' });
      const request = stateRoot.focusSnapshot.context.request;
      try {
        const snapshot = await runContextSwitch(() => api.newChat());
        openMintedSession(snapshot, request);
      } catch (err) {
        stateRoot.sendFocus({
          type: 'OPEN_FAILED',
          request,
          error: errorMessage(err),
        });
      }
      void get().loadSessions();
    },

    openDraftChat: () => {
      // A new chat starts as an unsent draft: no session is minted
      // until the first message picks a workspace and sends.
      stateRoot.sendFocus({ type: 'OPEN_DRAFT' });
      set({ toolsView: null, configOpen: false });
    },

    backFromFailure: () => {
      stateRoot.sendFocus({ type: 'BACK' });
    },

    resume: (id) =>
      measureInteraction('resume', async () => {
        clearTurnAttention(id);
        if (activeConversationID() === id) {
          // Returning to the already-active conversation means closing
          // whatever overlay/tool page currently covers the chat.
          set({ toolsView: null, configOpen: false });
          return;
        }
        if (stateRoot.registry.isDeleted(id)) {
          // This id was deleted in this process and the backend never
          // reuses a session id, so there is nothing to resume. Saying
          // so beats switching focus to a conversation whose actor can
          // never be created (the registry refuses tombstoned ids): the
          // chat would sit on "loading history" with no event able to
          // finish it.
          get().toast(i18n.t('chat.sessionDeleted'), 'warning');
          return;
        }
        stateRoot.sendFocus({ type: 'OPEN_SESSION', id });
        const request = stateRoot.focusSnapshot.context.request;
        try {
          const snapshot = await runContextSwitch(() => api.resumeSession(id));
          stateRoot.sendFocus({
            type: 'OPEN_SUCCEEDED',
            request,
            sessionID: snapshot.conversation_id,
          });
          const focus = stateRoot.focusSnapshot;
          if (
            focus.value !== 'active' ||
            focus.context.sessionID !== snapshot.conversation_id
          ) {
            return;
          }
          const resolvedID = snapshot.conversation_id;
          const actor = stateRoot.registry.ensure(resolvedID, {
            workspaceGeneration: stateRoot.generation(),
            workspace: get().workspace,
          });
          const hydrateRequest = 1;
          const generation = stateRoot.generation();
          actor?.send({
            type: 'HYDRATE_REQUESTED',
            request: hydrateRequest,
            generation,
          });
          const existing = get().conversations[resolvedID];
          const actorValue = actor?.getSnapshot().value as
            { transcript: string; turn: string } | undefined;
          if (existing && actorValue?.transcript === 'ready') {
            set({
              toolsView: null,
              conversations: {
                ...get().conversations,
                [resolvedID]: {
                  ...get().conversations[resolvedID],
                  mode: snapshot.mode,
                  think: snapshot.think,
                  model: snapshot.model,
                },
              },
            });
            actor?.send({
              type: 'HYDRATE_OK',
              request: hydrateRequest,
              generation,
              empty: existing.messages.length === 0,
            });
            retainLiveConversations(resolvedID);
            return;
          }
          let turns: Awaited<ReturnType<typeof api.sessionTurns>>;
          try {
            turns = await api.sessionTurns(
              resolvedID,
              INITIAL_HISTORY_TURNS + 1,
              0,
            );
          } catch (err) {
            actor?.send({
              type: 'HYDRATE_FAIL',
              request: hydrateRequest,
              generation,
              error: errorMessage(err),
            });
            if (!existing) {
              set((state) => ({
                conversations: {
                  ...state.conversations,
                  [resolvedID]: emptyConv(),
                },
              }));
            }
            return;
          }
          const page = historyPage(turns, INITIAL_HISTORY_TURNS);
          const archivedMessages = page.messages;
          // A live shell may already hold the current run's streamed
          // messages. Keep them after the archived history; completed
          // shells are replaced by the archive instead of duplicated.
          const keepLive =
            Boolean(existing) &&
            (actorValue?.turn === 'running' || actorValue?.turn === 'starting');
          // The steers the archive never saw travel across the rebuild
          // with the registrations that still owe them a boundary; they
          // are the only copy of their text (see
          // carryUnarchivedSteers).
          const carried = carryUnarchivedSteers(existing, archivedMessages);
          const mergedMessages = keepLive
            ? [...archivedMessages, ...existing.messages]
            : [...archivedMessages, ...carried.messages];
          // A pending steer row travels with the live run above, so the
          // registration naming its run has to travel too: turn_end
          // settles rows by registration, and a rebuilt shell that kept
          // the row but dropped the registration would leave it waiting
          // for a boundary that already came.
          const liveSteerSent = keepLive
            ? (existing?.steerSent ?? []).filter((s) =>
                mergedMessages.some(
                  (m) => m.id === s.messageID && m.steer === 'pending',
                ),
              )
            : carried.steerSent;
          // The live run owns every row past the seam the archive ends
          // at, so it gets the strip that owns them. Without one those
          // rows read as the last archived turn's: that turn's fold
          // header names a run that already finished and its steps
          // collapse under a complete answer (see turnIsLive in
          // ChatView). The strip carries the run the actor is tracking,
          // which is what the artifact and turn_end events look their
          // turn up by, and a strip the shell already had keeps its own
          // rows (see liveTurnEntry).
          const liveRunID = (
            actor?.getSnapshot().context as
              { currentRunID?: string } | undefined
          )?.currentRunID;
          const turnArtifacts = keepLive
            ? [
                ...page.turnArtifacts,
                liveTurnEntry(
                  archivedMessages.length,
                  existing.turnArtifacts[existing.turnArtifacts.length - 1],
                  liveRunID,
                ),
              ]
            : page.turnArtifacts;
          set((state) => ({
            toolsView: null,
            conversations: {
              ...state.conversations,
              [resolvedID]: settleConversation({
                ...emptyConv(),
                mode: snapshot.mode,
                think: snapshot.think,
                model: snapshot.model,
                messages: mergedMessages,
                turnArtifacts,
                historySeq: page.historySeq,
                historyHasMore: page.historyHasMore,
                pendingInteracts: existing?.pendingInteracts ?? [],
                ...(liveSteerSent.length > 0
                  ? { steerSent: liveSteerSent }
                  : {}),
              }),
            },
          }));
          if (existing?.pendingInteracts.length) {
            syncPendingIndex(resolvedID);
          }
          actor?.send({
            type: 'HYDRATE_OK',
            request: hydrateRequest,
            generation,
            empty: turns.length === 0 && !keepLive,
          });
          retainLiveConversations(resolvedID);
        } catch (err) {
          stateRoot.sendFocus({
            type: 'OPEN_FAILED',
            request,
            error: errorMessage(err),
          });
        }
      }),

    deleteSession: async (id) => {
      try {
        // Settle any queued deltas before deleting so a late flush
        // cannot resurrect the conversation after the tombstone.
        flushPendingStreams();
        // The backend stops any live run for the conversation and,
        // when it was current, mints its replacement in the same call.
        const next = await runContextSwitch(() => api.deleteSession(id));
        stateRoot.registry.get(id)?.send({
          type: 'SESSION_DELETED',
          deletedAt: new Date().toISOString(),
        });
        // The tombstone takes the conversation's run mappings with it:
        // a deleted chat never ends its turn, so the entries would keep
        // naming a conversation that is gone (and a late turn_end would
        // then reconcile it back onto the screen).
        stateRoot.registry.markDeleted(id);
        set((state) => {
          const conversations = { ...state.conversations };
          delete conversations[id];
          const viewers = { ...state.viewers };
          delete viewers[id];
          return { conversations, viewers };
        });
        clearPendingIndex(id);
        clearTurnAttention(id);
        // A tail sync waiting on this conversation holds its id until
        // that conversation's next turn_end; there will not be one.
        paging.forgetDeferredTailSync(id);
        // The backend only mints when the deleted chat was still the
        // workspace's current conversation once the delete settled.
        // Open that replacement only when this chat is still focused;
        // a selection made while the delete waited wins and should not
        // be stomped by an OPEN_NEW.
        if (next.conversation_id && activeConversationID() === id) {
          stateRoot.sendFocus({ type: 'OPEN_NEW' });
          openMintedSession(next, stateRoot.focusSnapshot.context.request);
        }
        await get().loadSessions();
      } catch (err) {
        set({ statusText: errorMessage(err) });
      }
    },

    loadSessions: async () => {
      set({ sessionsLoading: true });
      try {
        const sessions = (await api.listSessions()) ?? [];
        // An id deleted in this process never comes back on screen. The
        // listing can race the delete — a response computed before the
        // backend settled, a store restored out of band — and the
        // sidebar is the one place a dead entry would look openable
        // while every path into it is refused by the same tombstone.
        set({
          sessions: sessions.filter(
            (session) => !stateRoot.registry.isDeleted(session.id),
          ),
        });
      } catch {
        // best-effort
      } finally {
        set({ sessionsLoading: false });
      }
    },

    openSessionInWorkspace: async (sessionID, workspacePath) => {
      if (workspacePath && workspacePath !== get().workspace) {
        await api.openWorkspace(workspacePath);
        // The backend emits "ready" asynchronously relative to the
        // binding response; wait until the store has applied it so a
        // pending newChat is queued before resume tries to open the
        // target session in the new workspace.
        const landed = await switchWorkspace(workspacePath, 5000);
        if (!landed) {
          throw new Error('workspace switch did not complete');
        }
        if (!sameWorkspacePath(landed, workspacePath)) {
          // Another workspace's switch took the window while this one
          // was in flight: the click that is on screen now wins.
          return;
        }
        void get().loadWorkspaces();
        await waitForWorkspaceRestore();
        if (get().workspace !== landed) {
          // The window left again while this switch (and its restore)
          // was in flight: another workspace was picked in the
          // meantime. Resuming now would run through bindings that read
          // whatever workspace is active, so the click still on screen
          // wins and this one is dropped.
          return;
        }
      }
      await get().resume(sessionID);
    },

    /**
     * openFromNotification reopens the conversation a system banner
     * belongs to. The click arrives from outside the app: the window may
     * be on another workspace, and it may be covered by a tool page or
     * the config panel. Both are closed before the hop rather than after
     * it: the click's answer is either the conversation or the failure
     * that says it could not open, and both are drawn on the chat the
     * page was covering. The hop itself is openSessionInWorkspace's — a
     * switch a newer click took still wins, and a workspace that never
     * landed still throws.
     */
    openFromNotification: async (sessionID, workspacePath) => {
      set({ toolsView: null, configOpen: false });
      try {
        if (workspacePath) {
          await get().openSessionInWorkspace(sessionID, workspacePath);
          return;
        }
        // A banner from a producer that could not name its workspace
        // still names the conversation, which is the one thing the
        // click promised: open it where the window already is. The
        // backend refuses an id this workspace's store does not own, so
        // the fallback lands on that refusal instead of adopting
        // another workspace's conversation as this one's.
        await get().resume(sessionID);
      } catch {
        get().toast(i18n.t('chat.switchFailed'), 'warning');
      }
    },
  };
}
