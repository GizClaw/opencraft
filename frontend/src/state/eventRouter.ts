import type { StateRoot } from './root';
import type { ConversationEvent } from './machines/conversation';
import type { UIEvent } from '../lib/types';

type FailedStatus = 'failed' | 'aborted' | 'canceled' | 'interrupted';

export interface EventDataSink {
  writeConversationData(conversationID: string, ev: UIEvent): void;
  writeSubagentStream(ev: UIEvent): void;
  writeGlobalData(ev: UIEvent): void;
  refreshSessionList(): void;
  refreshAutomations(): void;
  refreshAutomationRuns(ev: UIEvent): void;
  conversationForRunID(runID: string): string | undefined;
  pendingInteractConversation(promptID: string): string | undefined;
}

export interface EventRouterDeps {
  root: StateRoot;
  data: EventDataSink;
}

interface StreamPayload {
  run_id?: string;
  conversation_id?: string;
  delta?: {
    part?: {
      type?: string;
      call?: { name?: string };
    };
  };
}

interface TurnEndPayload {
  run_id?: string;
  conversation_id?: string;
  status: string;
  error?: string;
}

function streamStage(delta: StreamPayload['delta']): string | undefined {
  const part = delta?.part;
  const type = part?.type;
  if (type === 'reasoning') return 'reasoning';
  if (type === 'tool_call') return `tool:${part?.call?.name ?? ''}`;
  if (type === 'text') return 'text';
  return undefined;
}

/**
 * Maps a backend event to the corresponding conversation actor event,
 * or undefined for data-only events.
 */
export function toConversationEvent(ev: UIEvent): ConversationEvent | undefined {
  switch (ev.type) {
    case 'automation_run_started': {
      const data = ev.data as { run_id?: string };
      if (!data.run_id) return undefined;
      return { type: 'RUN_STARTED', runID: data.run_id };
    }
    case 'stream': {
      const data = ev.data as StreamPayload;
      if (!data.run_id) return undefined;
      return {
        type: 'STREAM',
        runID: data.run_id,
        stage: streamStage(data.delta),
      };
    }
    case 'turn_end': {
      const data = ev.data as TurnEndPayload;
      if (!data.run_id) return undefined;
      const status: 'completed' | FailedStatus =
        data.status === 'completed' ||
        !(
          data.status === 'failed' ||
          data.status === 'aborted' ||
          data.status === 'canceled' ||
          data.status === 'interrupted'
        )
          ? 'completed'
          : (data.status as FailedStatus);
      return {
        type: 'TURN_ENDED',
        runID: data.run_id,
        status,
        error: data.error,
      };
    }
    default:
      return undefined;
  }
}

export function canApplyEvent(
  projection: {
    lifecycle: { name: 'alive' | 'deleted' };
    turn: {
      name:
        | 'idle'
        | 'starting'
        | 'running'
        | 'succeeded'
        | 'failed';
    };
  },
  ev: UIEvent,
): boolean {
  if (projection.lifecycle.name === 'deleted') return false;
  switch (ev.type) {
    case 'stream':
      return (
        projection.turn.name === 'idle' ||
        projection.turn.name === 'starting' ||
        projection.turn.name === 'running'
      );
    case 'automation_run_started':
      return (
        projection.turn.name === 'idle' ||
        projection.turn.name === 'starting' ||
        projection.turn.name === 'running'
      );
    case 'turn_end':
      return (
        projection.turn.name === 'starting' ||
        projection.turn.name === 'running'
      );
    default:
      // artifact/interact/artifact_sync/resolved are data-only or are
      // reconciled by the data layer; actor validity is checked there.
      return true;
  }
}

function routeToConversation(
  conversationID: string,
  ev: UIEvent,
  deps: EventRouterDeps,
) {
  if (deps.root.registry.isDeleted(conversationID)) return;
  const actor = deps.root.registry.ensure(conversationID, {
    workspaceGeneration: deps.root.generation(),
  });
  if (!actor) return;

  // Data and state changes stay in one synchronous unit. State is
  // checked first so a late event never reaches the data layer.
  const snapshot = actor.getSnapshot();
  const value = snapshot.value as unknown as {
    lifecycle: string;
    transcript: string;
    turn: string;
  };
  const projection = {
    lifecycle: { name: value.lifecycle as 'alive' | 'deleted' },
    turn: { name: value.turn as 'idle' | 'starting' | 'running' | 'succeeded' | 'failed' },
  };
  if (!canApplyEvent(projection, ev)) return;

  deps.data.writeConversationData(conversationID, ev);
  const event = toConversationEvent(ev);
  if (event) actor.send(event);
}

/**
 * Replaces the old store.handleEvent switch. Global events go to the
 * data layer / future shell region; conversation events are validated
 * against the owning actor before touching data.
 */
export function routeBackendEvent(ev: UIEvent, deps: EventRouterDeps) {
  switch (ev.type) {
    case 'ready':
    case 'fatal':
    case 'status':
    case 'usage':
    case 'managed_restored':
      deps.data.writeGlobalData(ev);
      return;

    case 'stream':
    case 'artifact':
    case 'artifact_sync':
    case 'interact':
    case 'turn_end':
    case 'automation_run_started': {
      const data = ev.data as {
        conversation_id?: string;
        run_id?: string;
      };
      const conversationID =
        data.conversation_id ??
        (data.run_id
          ? deps.data.conversationForRunID(data.run_id)
          : undefined);
      if (conversationID) {
        routeToConversation(conversationID, ev, deps);
        return;
      }
      if (ev.type === 'stream' && data.run_id) {
        deps.data.writeSubagentStream(ev);
      }
      return;
    }

    case 'resolved': {
      const data = ev.data as { id?: string; conversation_id?: string };
      const conversationID =
        data.conversation_id ??
        (data.id
          ? deps.data.pendingInteractConversation(data.id)
          : undefined);
      if (conversationID) {
        routeToConversation(conversationID, ev, deps);
      }
      return;
    }

    case 'session_updated':
      deps.data.refreshSessionList();
      return;

    case 'automation_changed':
      deps.data.refreshAutomations();
      return;

    case 'automation_run':
      deps.data.refreshAutomations();
      deps.data.refreshAutomationRuns(ev);
      return;
  }
}
