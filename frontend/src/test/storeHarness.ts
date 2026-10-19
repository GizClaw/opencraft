// Shared setup for the store's regression net.
//
// The cases live next to the module they exercise (conv/*, shell/*), but
// every one of them drives the same composition root: `useStore` plus the
// state root's registry. This file holds the seeding those files all
// need, so a case reads as its scenario and its assertions - not as sixty
// lines of initial state.
import { vi } from 'vitest';
import { itemText, useStore } from '../lib/store';
import type { MessageView } from '../lib/store';
import { setUnsentDraft } from '../lib/unsentDraft';
import { stateRoot } from '../state/app';
import { apiMock } from './apiMock';

/** Puts the composition root back to the one-conversation fixture. */
export function resetStore() {
  stateRoot.resetWorkspace();
  stateRoot.sendFocus({ type: 'RESTORE_FOCUS', sessionID: 's-1' });
  stateRoot.registry.ensure('s-1', {
    workspaceGeneration: stateRoot.generation(),
    readyEmpty: true,
    workspace: '/tmp/w',
  });
  useStore.setState({
    status: {
      needed: false,
      default_model: 'm',
      default_reasoning: true,
      work_dir: '/tmp/w',
      user_dir: '/tmp/u',
      version: 'test',
      agents: 0,
    },
    configured: true,
    fatal: null,
    configOpen: false,
    configTab: 'general',
    toolsView: null,
    workspace: '/tmp/w',
    agents: [],
    sessions: [],
    automations: [],
    automationRuns: {},
    conversations: {
      's-1': {
        messages: [],
        turnArtifacts: [],
        mode: 'workspace',
        think: 'medium',
        model: '',
        pendingInteracts: [],
      },
    },
    pendingPromptConvs: {},
    composerDraft: '',
    statusText: '',
    lastUsage: null,
    modelOptions: [],
    sessionDefaults: { mode: 'workspace', think: 'medium' },
    yoloOnly: false,
    theme: 'dark',
    workspaces: [],
    toasts: [],
    sessionsLoading: false,
  });
}

/** The `beforeEach` every store test file installs. */
export function primeStoreTest() {
  resetStore();
  // The draft registry is a module, not store state, so resetStore cannot
  // reach it: a test file that leaves a draft behind would make the next
  // one's workspace switches skip their reload.
  setUnsentDraft(false);
  vi.clearAllMocks();
  // The backend is where the window believes it is: the switch guard asks
  // it once per re-pick (a case that wants them to disagree sets its own
  // answer).
  apiMock.workspace.mockResolvedValue('/tmp/w');
  apiMock.profile.mockResolvedValue({ yolo_only: false });
  apiMock.sessionDefaults.mockResolvedValue({
    mode: 'workspace',
    think: 'medium',
  });
  apiMock.startTurn.mockResolvedValue({
    run_id: 'r-1',
    context_id: 's-1',
  });
  apiMock.steerTurn.mockResolvedValue(undefined);
  apiMock.forkTurn.mockResolvedValue('s-fork');
  apiMock.deleteSession.mockResolvedValue({
    conversation_id: '',
    mode: '',
    think: '',
    model: '',
  });
  apiMock.newChat.mockResolvedValue({
    conversation_id: 's-new',
    mode: 'workspace',
    think: 'medium',
    model: '',
  });
  apiMock.resumeSession.mockResolvedValue({
    conversation_id: 's-2',
    mode: 'workspace',
    think: 'medium',
    model: '',
  });
  apiMock.listSessions.mockResolvedValue([]);
  apiMock.sessionTurns.mockResolvedValue([]);
  apiMock.turnsSince.mockResolvedValue([]);
  apiMock.turnByRunID.mockRejectedValue(new Error('archive turn not found'));
}

export function historyTurn(
  seq: number,
  userText: string,
  assistantText: string,
) {
  return {
    seq,
    at: '2026-09-03T00:00:00Z',
    messages: [
      {
        role: 'user',
        content: { parts: [{ type: 'text', text: userText }] },
      },
      {
        role: 'assistant',
        content: { parts: [{ type: 'text', text: assistantText }] },
      },
    ],
    artifacts: [],
  };
}

/** Reads every assistant row's visible text the way the transcript does. */
export function assistantTexts(messages: MessageView[]): string[] {
  return messages.flatMap((m) =>
    m.items
      .filter(
        (it): it is Extract<MessageView['items'][number], { kind: 'text' }> =>
          it.kind === 'text',
      )
      .map((it) => itemText(it)),
  );
}

export function actorValue(conversationID: string) {
  const actor = stateRoot.registry.get(conversationID);
  if (!actor) return undefined;
  return actor.getSnapshot().value as {
    lifecycle: string;
    transcript: string;
    turn: string;
  };
}

// A bound run is registry state, not store state (S1): the production
// paths bind a run where its id becomes known, so the cases below seed
// and read the index through the same door.
export function bindRun(runID: string, conversationID = 's-1') {
  stateRoot.registry.bindRun(runID, conversationID);
}

export function conversationForRun(runID: string) {
  return stateRoot.registry.conversationForRun(runID);
}
