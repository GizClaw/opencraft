// Session navigation state machine. Pure module: no Zustand, no API.
export type SessionNavigation =
  | { name: 'idle'; epoch: number }
  | {
      name: 'ready';
      sessionID: string;
      epoch: number;
    }
  | {
      name: 'switching';
      kind: 'resume' | 'new';
      targetID?: string;
      previousSessionID?: string;
      epoch: number;
    }
  | {
      name: 'failed';
      epoch: number;
      previousSessionID?: string;
      error: string;
    };

export type SessionNavigationAction =
  | { type: 'idle'; epoch: number }
  | { type: 'ready'; sessionID: string; epoch: number }
  | {
      type: 'switch';
      kind: 'resume' | 'new';
      targetID?: string;
      previousSessionID?: string;
      epoch: number;
    }
  | {
      type: 'fail';
      epoch: number;
      previousSessionID?: string;
      error: string;
    };

export const navigationReducer = (
  state: SessionNavigation,
  action: SessionNavigationAction,
): SessionNavigation => {
  switch (action.type) {
    case 'idle':
      return { name: 'idle', epoch: action.epoch };
    case 'ready':
      return {
        name: 'ready',
        sessionID: action.sessionID,
        epoch: action.epoch,
      };
    case 'switch':
      return {
        name: 'switching',
        kind: action.kind,
        targetID: action.targetID,
        previousSessionID: action.previousSessionID,
        epoch: action.epoch,
      };
    case 'fail':
      return {
        name: 'failed',
        epoch: action.epoch,
        previousSessionID: action.previousSessionID,
        error: action.error,
      };
  }
};

export const activeSessionID = (nav: SessionNavigation) =>
  nav.name === 'ready' ? nav.sessionID : '';

// displaySessionID keeps the previously active session on screen while
// navigation is switching, so a history load never blanks the chat.
export const displaySessionID = (nav: SessionNavigation) => {
  if (nav.name === 'ready') return nav.sessionID;
  if (nav.name === 'switching' || nav.name === 'failed') {
    return nav.previousSessionID ?? '';
  }
  return '';
};
