import { StateRoot } from './root';

// The single root used by the live application. Tests create their own
// StateRoot instances so they never share state.
export const stateRoot = new StateRoot();
