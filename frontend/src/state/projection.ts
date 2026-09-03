import type {
  FocusState,
  LifecycleState,
  SessionRef,
  SessionTarget,
  TranscriptState,
  TurnState,
} from './types';

export interface ConversationViewState {
  lifecycle: LifecycleState;
  transcript: TranscriptState;
  turn: TurnState;
}

/**
 * projectFocus converts a sessionFocus snapshot into the logical
 * discriminated union components consume.
 */
export function projectFocus(snapshot: {
  value: string;
  context: {
    request: number;
    from: SessionRef;
    to: SessionTarget | null;
    sessionID: string;
    error: string;
  };
}): FocusState {
  switch (snapshot.value) {
    case 'no-session':
      return { name: 'no-session' };
    case 'opening': {
      const to =
        snapshot.context.to?.kind === 'existing' &&
        snapshot.context.to.id !== undefined
          ? { kind: 'existing' as const, id: snapshot.context.to.id }
          : { kind: 'new' as const };
      return {
        name: 'opening',
        request: snapshot.context.request,
        from: snapshot.context.from,
        to,
      };
    }
    case 'failed': {
      const to =
        snapshot.context.to?.kind === 'existing' &&
        snapshot.context.to.id !== undefined
          ? { kind: 'existing' as const, id: snapshot.context.to.id }
          : { kind: 'new' as const };
      return {
        name: 'failed',
        request: snapshot.context.request,
        from: snapshot.context.from,
        to,
        error: snapshot.context.error,
      };
    }
    case 'active':
      return { name: 'active', sessionID: snapshot.context.sessionID };
    default:
      return { name: 'no-session' };
  }
}

/**
 * projectConversation converts one parallel conversation snapshot
 * into the logical transcript/turn/lifecycle union. State names live
 * in state.value; metadata such as runID/error lives in context.
 */
export function projectConversation(snapshot: {
  value: {
    lifecycle: string;
    transcript: string;
    turn: string;
  };
  context: {
    emptyTranscript?: boolean;
    transcriptError?: string;
    currentRunID?: string;
    turnStage?: string;
    failureStatus?: 'failed' | 'aborted' | 'canceled' | 'interrupted';
    turnError?: string;
  };
}): ConversationViewState {
  const lifecycle: LifecycleState =
    snapshot.value.lifecycle === 'deleted'
      ? { name: 'deleted' }
      : { name: 'alive' };

  let transcript: TranscriptState;
  switch (snapshot.value.transcript) {
    case 'loading':
      transcript = { name: 'loading' };
      break;
    case 'ready':
      transcript = {
        name: 'ready',
        empty: snapshot.context.emptyTranscript ?? false,
      };
      break;
    case 'failed':
      transcript = {
        name: 'failed',
        error: snapshot.context.transcriptError ?? 'unknown error',
      };
      break;
    default:
      transcript = { name: 'unloaded' };
  }

  let turn: TurnState;
  switch (snapshot.value.turn) {
    case 'starting':
      turn = { name: 'starting' };
      break;
    case 'running':
      turn = {
        name: 'running',
        runID: snapshot.context.currentRunID ?? '',
        stage: snapshot.context.turnStage ?? '',
      };
      break;
    case 'succeeded':
      turn = { name: 'succeeded' };
      break;
    case 'failed':
      turn = {
        name: 'failed',
        status: snapshot.context.failureStatus ?? 'failed',
        error: snapshot.context.turnError,
      };
      break;
    default:
      turn = { name: 'idle' };
  }

  return { lifecycle, transcript, turn };
}
