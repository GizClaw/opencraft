import { useSyncExternalStore } from 'react';
import { stateRoot } from './app';
import {
  projectConversation,
  projectFocus,
  type ConversationViewState,
} from './projection';
import type { FocusState } from './types';

const subscribeRoot = (callback: () => void) => stateRoot.subscribe(callback);
const getVersion = () => stateRoot.getVersion();

/**
 * Re-renders whenever the root focus actor or any conversation actor
 * changes. StateRoot publishes one version per actor/registry update,
 * so components can read fresh projections after every XState event.
 */
export function useRootVersion(): number {
  return useSyncExternalStore(subscribeRoot, getVersion);
}

export function useFocusState(): FocusState {
  useRootVersion();
  const snapshot = stateRoot.focusSnapshot as unknown as Parameters<
    typeof projectFocus
  >[0];
  return projectFocus(snapshot);
}

export function useActiveConversationId(): string | undefined {
  const focus = useFocusState();
  return focus.name === 'active' ? focus.sessionID : undefined;
}

export function useConversationState(
  conversationID?: string,
): ConversationViewState | undefined {
  useRootVersion();
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
  if (!id) return undefined;
  const actor = stateRoot.registry.get(id);
  if (!actor) return undefined;
  return projectConversation(
    actor.getSnapshot() as unknown as Parameters<
      typeof projectConversation
    >[0],
  );
}

export function useRunningConversations(): Array<{
  conversationID: string;
  state: ConversationViewState;
}> {
  useRootVersion();
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
    if (
      state.turn.name === 'starting' ||
      state.turn.name === 'running'
    ) {
      out.push({ conversationID: actor.getSnapshot().context.id, state });
    }
  }
  return out;
}
