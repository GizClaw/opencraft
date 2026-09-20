import { Component } from 'react';
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
}

interface State {
  error: Error | null;
}

// ErrorBoundary turns an uncaught render error into a visible crash
// card instead of a silent white screen, so failures are diagnosable
// and recoverable without restarting the app.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    reportFrontendError(
      this.props.scope ?? 'react-render',
      error,
      info.componentStack ?? '',
    );
  }

  render() {
    if (this.state.error) {
      if (this.props.inline) {
        return (
          <div className="rounded-control border border-err/40 bg-err/5 px-3 py-2 text-xs">
            <p className="font-medium text-err">
              {i18n.t('app.surfaceFailed')}
            </p>
            <p className="mt-1 break-words text-dim">
              {String(this.state.error?.message ?? this.state.error)}
            </p>
            <button
              onClick={() => this.setState({ error: null })}
              className="mt-2 rounded-tight border border-edge px-2 py-0.5 text-dim hover:text-fg"
            >
              {i18n.t('app.tryAgain')}
            </button>
          </div>
        );
      }
      return (
        <div className="h-full grid place-items-center bg-bg px-6">
          <div className="max-w-lg rounded-card border border-err/40 bg-panel p-6 shadow-modal">
            <h1 className="text-base font-semibold text-err">
              {i18n.t('app.crashed')}
            </h1>
            <p className="mt-2 text-sm text-dim break-words">
              {String(this.state.error?.message ?? this.state.error)}
            </p>
            <pre className="mt-3 max-h-48 overflow-auto rounded-card bg-panel2 border border-edge p-3 text-xs text-dim whitespace-pre-wrap">
              {this.state.error?.stack}
            </pre>
            <div className="mt-4 flex justify-end gap-2">
              <button
                onClick={() => this.setState({ error: null })}
                className="rounded-control border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
              >
                {i18n.t('app.tryAgain')}
              </button>
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
    return this.props.children;
  }
}
