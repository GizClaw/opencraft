import i18n from '../../i18n';
import { TurnStatus } from './model';

// friendlyInterruption maps the engine's interrupt cause to user-facing
// text so raw engine internals never leak into the transcript. The cause
// is a field the backend reads from the engine's typed interrupt; the UI
// does not parse the error text. It returns null when the turn did not
// end as an interrupt, so the original error stays.
export function friendlyInterruption(cause?: string): string | null {
  switch (cause) {
    case 'host_shutdown':
      return i18n.t('chat.interruptedHostShutdown');
    case 'user_cancel':
      return i18n.t('chat.cancelled');
    case 'user_input':
      return i18n.t('chat.interruptedUserInput');
    case 'app_restart':
      // The only cause the engine cannot classify itself: the process
      // was gone before it could, so the turn came back as an archived
      // interrupted one on the next assembly (host/recover.go).
      return i18n.t('chat.interruptedAppRestart');
    case undefined:
    case '':
      // The engine's zero cause ("unknown") renders as empty.
      return null;
    default:
      return i18n.t('chat.interrupted');
  }
}

// isUserStop reports whether a non-completed turn ended because the
// user stopped it (the cancel button or a barge-in user message), so
// the turn-end notice can stay concise and provider diagnostics can
// be hidden. The engine reports a deadline and a user stop as the same
// `canceled` status; errorKind is what tells them apart, and a turn
// the deadline ended keeps its diagnostics (and its own words).
export function isUserStop(
  status: TurnStatus | undefined,
  interruptCause?: string,
  errorKind?: string,
): boolean {
  if (status === 'canceled') return errorKind !== 'timeout';
  if (status !== 'interrupted') return false;
  return interruptCause === 'user_cancel' || interruptCause === 'user_input';
}

// turnNeedsAttention reports whether a finished turn is something the
// reader has to come back to, which is what the sidebar's ❌ means. It is
// isUserStop's inverse plus the one branch that inverse gets wrong for
// this purpose: a deadline stops a turn the user did not stop, so it is a
// failure worth opening. `interrupted` is not one — the host was shutting
// down or the app restarted, and the transcript already says so where the
// turn sits. A status this build cannot name counts as a failure for the
// same reason turnEndStatus draws it as one: the row and the turn have to
// agree about what happened.
export function turnNeedsAttention(
  status: TurnStatus | string | undefined,
  errorKind?: string,
): boolean {
  switch (status) {
    case 'completed':
    case 'interrupted':
      return false;
    case 'canceled':
      return errorKind === 'timeout';
    default:
      return true;
  }
}

// friendlyFailure maps the inference error kind to user-safe text: a
// provider failure only needs to tell the user the model call did not go
// through and that retrying is reasonable. It returns null for a failure
// the engine did not classify, so the caller can fall back to the raw
// error rather than claim a generic cause.
export function friendlyFailure(kind?: string): string | null {
  switch (kind) {
    case 'provider_failure':
      return i18n.t('chat.providerFailure');
    case 'invalid_provider_response':
      return i18n.t('chat.invalidProviderResponse');
    case 'unknown_provider':
    case 'unknown_model':
    case 'unknown_profile':
      return i18n.t('chat.modelConfiguration');
    case 'invalid_request':
      return i18n.t('chat.invalidRequest');
    case 'timeout':
      return i18n.t('chat.turnTimeout');
    case undefined:
    case '':
      return null;
    default:
      return i18n.t('chat.genericFailure');
  }
}
