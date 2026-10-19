import { UIEventType } from '../lib/events';
import type { StateRoot } from './root';
import { isEndedRun, type ConversationEvent } from './machines/conversation';
import type { UIEvent } from '../lib/types';

type FailedStatus = 'failed' | 'aborted' | 'canceled' | 'interrupted';

// Events components subscribe to on the channel themselves; the shell
// routes none of them (see routeBackendEvent's default branch).
const COMPONENT_EVENTS: readonly string[] = [
  UIEventType.gitChanged,
  UIEventType.inferenceChanged,
  UIEventType.telemetryChanged,
  UIEventType.petPacksChanged,
  UIEventType.petSettingsChanged,
  UIEventType.petRuntimeStatus,
];

// turnEndStatus maps the wire status of a finished turn onto the
// machine's terminal states. Completion is the one status that has to be
// named: everything else is a failure, including the backend's "unknown"
// — the engine status of a run whose wait produced no result, which the
// host records as a failed run (host.ClassifySettledRun). Reading it as
// success would show a turn that never finished as an answer, and would
// drop the error_kind the failure text is picked from.
function turnEndStatus(status: string): 'completed' | FailedStatus {
  switch (status) {
    case 'completed':
      return 'completed';
    case 'failed':
    case 'aborted':
    case 'canceled':
    case 'interrupted':
      return status;
    default:
      return 'failed';
  }
}

export interface EventDataSink {
  writeConversationData(conversationID: string, ev: UIEvent): void;
  writeGlobalData(ev: UIEvent): void;
  refreshSessionList(): void;
  /**
   * sessionUpdated reports that one conversation changed on the backend
   * outside its live stream (a turn the app appended on its own). The
   * data layer decides whether the transcript needs it.
   */
  sessionUpdated?(id: string): void;
  /**
   * openConversation asks the window to show one conversation: the user
   * activated a system notification about it. The workspace is the one
   * the backend said owns the run, and the window may not be on it.
   */
  openConversation?(id: string, workspace?: string): void;
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
  workspace?: string;
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
  workspace?: string;
  status: string;
  error?: string;
  error_kind?: string;
}

/**
 * eventWorkspace returns the workspace an event says owns its run, when
 * it says one. The backend stamps it on the three events that carry a
 * run (stream, interact, turn_end) because the window may be looking at
 * another workspace by the time any of them arrives.
 */
function eventWorkspace(ev: UIEvent): string | undefined {
  switch (ev.type) {
    case UIEventType.stream:
    case UIEventType.interact:
    case UIEventType.turnEnd: {
      const workspace = (ev.data as { workspace?: unknown }).workspace;
      return typeof workspace === 'string' && workspace !== ''
        ? workspace
        : undefined;
    }
    default:
      return undefined;
  }
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
      return {
        type: 'TURN_ENDED',
        runID: data.run_id,
        status: turnEndStatus(data.status),
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
    endedRunIDs?: string[];
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
  // run whose ending the actor has already seen; everything else is the
  // actor's call. That memory is a set, not one slot: a barge-in can
  // leave an older ending behind without it ever becoming the last one.
  switch (ev.type) {
    case UIEventType.stream:
    case UIEventType.automationRunStarted:
    case UIEventType.turnEnd: {
      const runID = (ev.data as { run_id?: string }).run_id;
      if (runID === undefined) return true;
      // The run's own terminal event passes here because the run is
      // not remembered yet - it is what puts it in the memory. A
      // replay of it, and every event after it, is dropped.
      return !isEndedRun(projection, runID);
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
  const bound = eventWorkspace(ev);
  const actor = deps.root.registry.ensure(conversationID, {
    workspaceGeneration: deps.root.generation(),
    workspace: bound ?? deps.data.activeWorkspace?.(),
  });
  if (!actor) return;
  // ensure() is a get-or-create: an actor this client already holds keeps
  // the options it was made with. The backend naming a workspace is the
  // authority on where the row belongs, so a conversation adopted from a
  // background run — registered under whatever workspace the window was
  // on at the time — is rebound here instead of staying where it landed.
  if (bound && actor.getSnapshot().context.workspace !== bound) {
    actor.send({ type: 'BIND_WORKSPACE', workspace: bound });
  }

  // Data and state changes stay in one synchronous unit. State is
  // checked first so a late event never reaches the data layer.
  const snapshot = actor.getSnapshot();
  const value = snapshot.value as unknown as { lifecycle: string };
  const projection = {
    lifecycle: { name: value.lifecycle as 'alive' | 'deleted' },
    endedRunIDs: (snapshot.context as unknown as { endedRunIDs?: string[] })
      .endedRunIDs,
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

    case UIEventType.openConversation: {
      const data = ev.data as { conversation_id?: string; workspace?: string };
      if (data.conversation_id) {
        deps.data.openConversation?.(data.conversation_id, data.workspace);
      }
      return;
    }

    case UIEventType.automationChanged:
      deps.data.refreshAutomations();
      return;

    case UIEventType.automationRun:
      deps.data.refreshAutomations();
      deps.data.refreshAutomationRuns(ev);
      return;

    default:
      // A backend event the shell does not route is one of two things:
      // an event a component subscribes to on its own (the git panels,
      // the config page, the pet windows — listed here), or an event
      // nobody handles at all. The second kind used to vanish silently,
      // which is how a renamed wire event stays invisible until someone
      // notices the feature is dead; in dev it says so.
      if (!COMPONENT_EVENTS.includes(ev.type) && import.meta.env.DEV) {
        console.warn(`eventRouter: no route for event "${ev.type}"`);
      }
  }
}
