import { assign, createMachine } from 'xstate';
import type { SessionRef, SessionTarget } from '../types';

export interface FocusInput {
  sessionID?: string;
}

interface FocusContext {
  request: number;
  from: SessionRef;
  to: SessionTarget | null;
  sessionID: string;
  error: string;
}

export type FocusEvent =
  | { type: 'OPEN_NEW' }
  | { type: 'OPEN_SESSION'; id: string }
  | { type: 'OPEN_SUCCEEDED'; request: number; sessionID: string }
  | { type: 'OPEN_FAILED'; request: number; error: string }
  | { type: 'RESTORE_FOCUS'; sessionID: string }
  | { type: 'BACK' }
  | { type: 'WORKSPACE_RESET' };

const reset = (): FocusContext => ({
  request: 0,
  from: { kind: 'none' },
  to: null,
  sessionID: '',
  error: '',
});

export const sessionFocusMachine = createMachine({
  id: 'sessionFocus',
  types: {} as {
    context: FocusContext;
    events: FocusEvent;
    input: FocusInput;
  },
  context: ({ input }) =>
    input.sessionID
      ? {
          ...reset(),
          from: { kind: 'session', id: input.sessionID },
          sessionID: input.sessionID,
        }
      : reset(),
  initial: 'no-session',
  states: {
    'no-session': {
      on: {
        OPEN_NEW: {
          target: 'opening',
          actions: assign({
            request: ({ context }) => context.request + 1,
            to: () => ({ kind: 'new' as const }),
          }),
        },
        OPEN_SESSION: {
          target: 'opening',
          actions: assign({
            request: ({ context }) => context.request + 1,
            to: ({ event }) => ({ kind: 'existing' as const, id: event.id }),
          }),
        },
        RESTORE_FOCUS: {
          target: 'active',
          actions: assign({
            sessionID: ({ event }) => event.sessionID,
            from: ({ event }) => ({
              kind: 'session' as const,
              id: event.sessionID,
            }),
          }),
        },
        WORKSPACE_RESET: {
          target: 'no-session',
          actions: assign(reset),
        },
      },
    },
    opening: {
      on: {
        OPEN_NEW: {
          target: 'opening',
          actions: assign({
            request: ({ context }) => context.request + 1,
            to: () => ({ kind: 'new' as const }),
          }),
        },
        OPEN_SESSION: {
          target: 'opening',
          actions: assign({
            request: ({ context }) => context.request + 1,
            to: ({ event }) => ({ kind: 'existing' as const, id: event.id }),
          }),
        },
        OPEN_SUCCEEDED: {
          guard: ({ context, event }) => event.request === context.request,
          target: 'active',
          actions: assign({
            sessionID: ({ event }) => event.sessionID,
            from: ({ event }) => ({
              kind: 'session' as const,
              id: event.sessionID,
            }),
            to: () => null,
            error: () => '',
          }),
        },
        OPEN_FAILED: {
          guard: ({ context, event }) => event.request === context.request,
          target: 'failed',
          actions: assign({
            error: ({ event }) => event.error,
          }),
        },
        WORKSPACE_RESET: {
          target: 'no-session',
          actions: assign(reset),
        },
      },
    },
    active: {
      on: {
        OPEN_NEW: {
          target: 'opening',
          actions: assign({
            request: ({ context }) => context.request + 1,
            to: () => ({ kind: 'new' as const }),
          }),
        },
        OPEN_SESSION: [
          {
            // Clicking the already-active session is a no-op.
            guard: ({ context, event }) => event.id === context.sessionID,
          },
          {
            target: 'opening',
            actions: assign({
              request: ({ context }) => context.request + 1,
              to: ({ event }) => ({
                kind: 'existing' as const,
                id: event.id,
              }),
            }),
          },
        ],
        RESTORE_FOCUS: {
          target: 'active',
          actions: assign({
            sessionID: ({ event }) => event.sessionID,
            from: ({ event }) => ({
              kind: 'session' as const,
              id: event.sessionID,
            }),
          }),
        },
        WORKSPACE_RESET: {
          target: 'no-session',
          actions: assign(reset),
        },
      },
    },
    failed: {
      on: {
        OPEN_NEW: {
          target: 'opening',
          actions: assign({
            request: ({ context }) => context.request + 1,
            to: () => ({ kind: 'new' as const }),
          }),
        },
        OPEN_SESSION: {
          target: 'opening',
          actions: assign({
            request: ({ context }) => context.request + 1,
            to: ({ event }) => ({ kind: 'existing' as const, id: event.id }),
          }),
        },
        BACK: [
          {
            guard: ({ context }) => context.from.kind === 'session',
            target: 'active',
            actions: assign({
              sessionID: ({ context }) =>
                context.from.kind === 'session' ? context.from.id : '',
              to: () => null,
              error: () => '',
            }),
          },
          { target: 'no-session' },
        ],
        WORKSPACE_RESET: {
          target: 'no-session',
          actions: assign(reset),
        },
      },
    },
  },
});
