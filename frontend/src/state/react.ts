import { useSyncExternalStore } from 'react';
import { stateRoot } from './app';
import {
  projectConversation,
  projectFocus,
  type ConversationViewState,
} from './projection';
import type { FocusState } from './types';

const subscribeRoot = (callback: () => void) => stateRoot.subscribe(callback);

// The signature helpers below subscribe to the same root version but
// return a derived string. useSyncExternalStore only re-renders when the
// returned value changes, so a panel that reads focus, one
// conversation's turn phase, or the running set stops re-rendering on
// every stream delta — those bump the version dozens of times a second
// while streaming, even though nothing this panel shows moved.
function focusSignature(): string {
  const snapshot = stateRoot.focusSnapshot as unknown as {
    value: string;
    context: {
      request: number;
      sessionID: string;
      error: string;
      from?: unknown;
      to?: { kind: string; id?: string } | null;
    };
  };
  const to = snapshot.context.to;
  return [
    snapshot.value,
    snapshot.context.request,
    snapshot.context.sessionID,
    snapshot.context.error,
    JSON.stringify(snapshot.context.from ?? null),
    to ? `${to.kind}:${to.id ?? ''}` : '',
  ].join('|');
}

function conversationSignature(id: string): string {
  const actor = stateRoot.registry.get(id);
  if (!actor) return '';
  const snapshot = actor.getSnapshot() as unknown as {
    value: { lifecycle: string; transcript: string; turn: string };
    context: {
      emptyTranscript?: boolean;
      transcriptError?: string;
      currentRunID?: string;
      turnStage?: string;
      supersededRunID?: string;
      failureStatus?: string;
      turnError?: string;
    };
  };
  const { value, context } = snapshot;
  return [
    value.lifecycle,
    value.transcript,
    value.turn,
    context.emptyTranscript ? '1' : '0',
    context.transcriptError ?? '',
    context.currentRunID ?? '',
    context.turnStage ?? '',
    context.supersededRunID ?? '',
    context.failureStatus ?? '',
    context.turnError ?? '',
  ].join('|');
}

function runningSignature(): string {
  const parts: string[] = [];
  for (const actor of stateRoot.registry.all()) {
    const snapshot = actor.getSnapshot() as unknown as {
      value: { turn: string };
      context: { id: string; workspace?: string };
    };
    const turn = snapshot.value.turn;
    if (turn !== 'starting' && turn !== 'running') continue;
    parts.push(
      `${snapshot.context.id}|${snapshot.context.workspace ?? ''}|${conversationSignature(snapshot.context.id)}`,
    );
  }
  parts.sort();
  return parts.join('\n');
}

export function useFocusState(): FocusState {
  useSyncExternalStore(subscribeRoot, focusSignature);
  const snapshot = stateRoot.focusSnapshot as unknown as Parameters<
    typeof projectFocus
  >[0];
  return projectFocus(snapshot);
}

export function conversationWorkspace(
  conversationID: string,
): string | undefined {
  return stateRoot.workspaceOf(conversationID);
}

export function useConversationState(
  conversationID?: string,
): ConversationViewState | undefined {
  const id =
    conversationID ??
    (() => {
      const focus = projectFocus(
        stateRoot.focusSnapshot as unknown as Parameters<
          typeof projectFocus
        >[0],
      );
      return focus.name === 'active' ? focus.sessionID : undefined;
    })();
  useSyncExternalStore(subscribeRoot, () =>
    id ? conversationSignature(id) : '',
  );
  if (!id) return undefined;
  const actor = stateRoot.registry.get(id);
  if (!actor) return undefined;
  return projectConversation(
    actor.getSnapshot() as unknown as Parameters<typeof projectConversation>[0],
  );
}

export function useRunningConversations(workspace?: string): Array<{
  conversationID: string;
  state: ConversationViewState;
}> {
  useSyncExternalStore(subscribeRoot, runningSignature);
  const out: Array<{
    conversationID: string;
    state: ConversationViewState;
  }> = [];
  for (const actor of stateRoot.registry.all()) {
    const state = projectConversation(
      actor.getSnapshot() as unknown as Parameters<
        typeof projectConversation
      >[0],
    );
    const actorWorkspace = actor.getSnapshot().context.workspace as
      string | undefined;
    const active =
      state.turn.name === 'starting' || state.turn.name === 'running';
    if (active && (workspace === undefined || actorWorkspace === workspace)) {
      out.push({ conversationID: actor.getSnapshot().context.id, state });
    }
  }
  return out;
}
