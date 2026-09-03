// Logical state projections for the XState layer. The state values on
// these discriminated unions include request/run metadata as a design
// shorthand; the machines keep that metadata in context instead of
// duplicating it in `state.value`.

export type SessionRef =
  | { kind: 'none' }
  | { kind: 'session'; id: string };

export type SessionTarget =
  | { kind: 'new' }
  | { kind: 'existing'; id: string };

export type TranscriptState =
  | { name: 'unloaded' }
  | { name: 'loading' }
  | { name: 'ready'; empty: boolean }
  | { name: 'failed'; error: string };

export type TurnEndKind =
  | 'failed'
  | 'aborted'
  | 'canceled'
  | 'interrupted';

export type TurnState =
  | { name: 'idle' }
  | { name: 'starting' }
  | { name: 'running'; runID: string; stage: string }
  | { name: 'succeeded' }
  | { name: 'failed'; status: TurnEndKind; error?: string };

export type LifecycleState =
  | { name: 'alive' }
  | { name: 'deleted' };

export type FocusState =
  | { name: 'no-session' }
  | { name: 'opening'; request: number; from: SessionRef; to: SessionTarget }
  | { name: 'active'; sessionID: string }
  | {
      name: 'failed';
      request: number;
      from: SessionRef;
      to: SessionTarget;
      error: string;
    };
