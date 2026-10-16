import { Component, Fragment } from 'react';
import type { ErrorInfo, ReactNode } from 'react';
import i18n from '../i18n';
import { reportFrontendError } from '../lib/frontendErrors';

interface Props {
  children: ReactNode;
  // scope names the failing surface in the report. Plugin surfaces pass
  // their plugin id so a crash is attributable without a stack dive.
  scope?: string;
  // inline renders a compact card in place of the children instead of the
  // full-window crash card. A plugin panel that throws on render must not
  // blank the whole app — or, worse, keep React remounting the tree.
  inline?: boolean;
  // onRetry runs before a retry remounts the children. A surface whose
  // render depends on bad data (a payload the backend sent, a stale
  // selection) has to drop that data here: remounting the same state
  // just renders the same crash again.
  onRetry?: () => void;
  // maxRetries is how many times the *same* failure may be retried
  // before the card stops offering "Try again" — a deterministic error
  // crashes again on every attempt — and points at Reload instead.
  maxRetries?: number;
}

interface State {
  error: Error | null;
  // Bumped by every retry: it remounts the children from scratch.
  attempt: number;
  // Consecutive failures carrying the same message.
  retries: number;
  lastMessage: string;
}

// ErrorBoundary turns an uncaught render error into a visible crash
// card instead of a silent white screen, so failures are diagnosable
// and recoverable without restarting the app.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, attempt: 0, retries: 0, lastMessage: '' };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  private get maxRetries(): number {
    return this.props.maxRetries ?? 2;
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    const message = String(error?.message ?? error);
    const repeated = message === this.state.lastMessage;
    this.setState({
      lastMessage: message,
      retries: repeated ? this.state.retries + 1 : 1,
    });
    reportFrontendError(
      this.props.scope ?? 'react-render',
      error,
      info.componentStack ?? '',
    );
  }

  componentDidUpdate(_prevProps: Props, prevState: State) {
    // The retry rendered the children without throwing, so the failure
    // is behind us and the next one starts counting from zero.
    if (this.state.error === null && prevState.error !== null) {
      this.setState({ retries: 0, lastMessage: '' });
    }
  }

  private retry = () => {
    // Drop the state that made the render throw before remounting: the
    // children are rebuilt under a new key, and the surface may need to
    // clear what it cached off the same bad input.
    this.props.onRetry?.();
    this.setState((state) => ({ error: null, attempt: state.attempt + 1 }));
  };

  render() {
    const error = this.state.error;
    if (!error) {
      // Keyed so a retry remounts the surface instead of re-rendering
      // the element tree that threw.
      return (
        <Fragment key={this.state.attempt}>{this.props.children}</Fragment>
      );
    }
    const detail = String(error.message ?? error);
    // Repeating the same failure is deterministic: the data has not
    // changed, so another "Try again" would only crash again.
    const retryable = this.state.retries <= this.maxRetries;

    if (this.props.inline) {
      return (
        <div className="rounded-control border border-err/40 bg-err/5 px-3 py-2 text-xs">
          <p className="font-medium text-err">{i18n.t('app.surfaceFailed')}</p>
          <p className="mt-1 break-words text-dim">{detail}</p>
          <div className="mt-2 flex gap-2">
            {retryable && (
              <button
                onClick={this.retry}
                className="rounded-tight border border-edge px-2 py-0.5 text-dim hover:text-fg"
              >
                {i18n.t('app.tryAgain')}
              </button>
            )}
            <button
              onClick={() => window.location.reload()}
              className="rounded-tight border border-edge px-2 py-0.5 text-dim hover:text-fg"
            >
              {i18n.t('app.reload')}
            </button>
          </div>
        </div>
      );
    }

    return (
      <div className="h-full grid place-items-center bg-bg px-6">
        <div className="max-w-lg rounded-card border border-err/40 bg-panel p-6 shadow-modal">
          <h1 className="text-base font-semibold text-err">
            {i18n.t('app.crashed')}
          </h1>
          <p className="mt-2 text-sm text-dim break-words">{detail}</p>
          <pre className="mt-3 max-h-48 overflow-auto rounded-card bg-panel2 border border-edge p-3 text-xs text-dim whitespace-pre-wrap">
            {error.stack}
          </pre>
          <div className="mt-4 flex justify-end gap-2">
            {retryable && (
              <button
                onClick={this.retry}
                className="rounded-control border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
              >
                {i18n.t('app.tryAgain')}
              </button>
            )}
            <button
              onClick={() => window.location.reload()}
              className="rounded-control bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90"
            >
              {i18n.t('app.reload')}
            </button>
          </div>
        </div>
      </div>
    );
  }
}
