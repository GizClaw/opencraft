import { UIEventType } from '../lib/events';
import type { StateRoot } from './root';
import type { ConversationActor } from './actorRegistry';
import type { ConversationEvent } from './machines/conversation';
import type { UIEvent } from '../lib/types';

type FailedStatus = 'failed' | 'aborted' | 'canceled' | 'interrupted';

export interface EventDataSink {
  syncConversationActor?(
    conversationID: string,
    actor: ConversationActor,
  ): void;
  writeConversationData(conversationID: string, ev: UIEvent): void;
  writeGlobalData(ev: UIEvent): void;
  refreshSessionList(): void;
  /**
   * sessionUpdated reports that one conversation changed on the backend
   * outside its live stream (a turn the app appended on its own). The
   * data layer decides whether the transcript needs it.
   */
  sessionUpdated?(id: string): void;
  refreshAutomations(): void;
  refreshAutomationRuns(ev: UIEvent): void;
  conversationForRunID(runID: string): string | undefined;
  pendingInteractConversation(promptID: string): string | undefined;
  activeWorkspace?(): string;
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
  error_kind?: string;
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
export function toConversationEvent(
  ev: UIEvent,
): ConversationEvent | undefined {
  switch (ev.type) {
    case UIEventType.automationRunStarted: {
      const data = ev.data as { run_id?: string };
      if (!data.run_id) return undefined;
      return { type: 'RUN_STARTED', runID: data.run_id };
    }
    case UIEventType.stream: {
      const data = ev.data as StreamPayload;
      if (!data.run_id) return undefined;
      return {
        type: 'STREAM',
        runID: data.run_id,
        stage: streamStage(data.delta),
      };
    }
    case UIEventType.turnEnd: {
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
        errorKind: data.error_kind,
      };
    }
    default:
      return undefined;
  }
}

export function canApplyEvent(
  projection: {
    lifecycle: { name: 'alive' | 'deleted' };
    lastEndedRunID?: string;
  },
  ev: UIEvent,
): boolean {
  if (projection.lifecycle.name === 'deleted') return false;
  // Whether an event still belongs to the conversation is a question
  // about the event's run, not about the phase this client last left the
  // turn in. The phase says where the UI thinks it is, and it knows
  // nothing about a run the UI never started: a scheduled automation
  // pinned to the conversation starts its turn whenever its schedule
  // fires, and a reload adopts a run that was already in flight. Gating
  // on the phase dropped those runs whole — the live text, the produced
  // files and the running row — and only a later `session_updated`
  // brought any of it back. The one genuinely stale event is one from a
  // run that has already ended; everything else is the actor's call.
  switch (ev.type) {
    case UIEventType.stream:
    case UIEventType.automationRunStarted:
    case UIEventType.turnEnd: {
      const data = ev.data as { run_id?: string };
      return (
        data.run_id === undefined || data.run_id !== projection.lastEndedRunID
      );
    }
    default:
      // artifact/interact/resolved are data-only or are reconciled by
      // the data layer; actor validity is checked there.
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
    workspace: deps.data.activeWorkspace?.(),
  });
  if (!actor) return;
  deps.data.syncConversationActor?.(conversationID, actor);

  // Data and state changes stay in one synchronous unit. State is
  // checked first so a late event never reaches the data layer.
  const snapshot = actor.getSnapshot();
  const value = snapshot.value as unknown as { lifecycle: string };
  const projection = {
    lifecycle: { name: value.lifecycle as 'alive' | 'deleted' },
    lastEndedRunID: (
      snapshot.context as unknown as { lastEndedRunID?: string }
    ).lastEndedRunID,
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
    case UIEventType.ready:
    case UIEventType.fatal:
    case UIEventType.status:
    case UIEventType.usage:
    case UIEventType.managedRestored:
      deps.data.writeGlobalData(ev);
      return;

    case UIEventType.stream:
    case UIEventType.artifact:
    case UIEventType.interact:
    case UIEventType.steerPending:
    case UIEventType.turnEnd:
    case UIEventType.automationRunStarted: {
      const data = ev.data as {
        conversation_id?: string;
        run_id?: string;
      };
      const conversationID =
        data.conversation_id ??
        (data.run_id ? deps.data.conversationForRunID(data.run_id) : undefined);
      if (conversationID) {
        routeToConversation(conversationID, ev, deps);
      }
      return;
    }

    case UIEventType.resolved: {
      const data = ev.data as { id?: string; conversation_id?: string };
      const conversationID =
        data.conversation_id ??
        (data.id ? deps.data.pendingInteractConversation(data.id) : undefined);
      if (conversationID) {
        routeToConversation(conversationID, ev, deps);
      }
      return;
    }

    case UIEventType.sessionUpdated: {
      deps.data.refreshSessionList();
      const data = ev.data as { id?: string };
      if (data.id) deps.data.sessionUpdated?.(data.id);
      return;
    }

    case UIEventType.automationChanged:
      deps.data.refreshAutomations();
      return;

    case UIEventType.automationRun:
      deps.data.refreshAutomations();
      deps.data.refreshAutomationRuns(ev);
      return;
  }
}
