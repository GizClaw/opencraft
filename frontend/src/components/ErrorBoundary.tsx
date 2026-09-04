import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';
import i18n from '../i18n';

interface Props {
  children: ReactNode;
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
    console.error('opencraft ui crash:', error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="h-full grid place-items-center bg-bg px-6">
          <div className="max-w-lg rounded-2xl border border-err/40 bg-panel p-6 shadow-2xl">
            <h1 className="text-base font-semibold text-err">
              {i18n.t('app.crashed')}
            </h1>
            <p className="mt-2 text-sm text-dim break-words">
              {String(this.state.error?.message ?? this.state.error)}
            </p>
            <pre className="mt-3 max-h-48 overflow-auto rounded-lg bg-panel2 border border-edge p-3 text-xs text-dim whitespace-pre-wrap">
              {this.state.error?.stack}
            </pre>
            <div className="mt-4 flex justify-end gap-2">
              <button
                onClick={() => this.setState({ error: null })}
                className="rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg"
              >
                {i18n.t('app.tryAgain')}
              </button>
              <button
                onClick={() => window.location.reload()}
                className="rounded-lg bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90"
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
