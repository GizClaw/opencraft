import React from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';
import './i18n';
import App from './App';
import { ErrorBoundary } from './components/ErrorBoundary';
import { installMockBridge } from './lib/mockBridge';
import {
  installConsoleErrorCapture,
  reportFrontendError,
} from './lib/frontendErrors';

// Surface uncaught errors instead of failing silently: render errors
// are caught by ErrorBoundary, event-handler errors and unhandled rejections
// are forwarded to the Go telemetry pipeline (and printed for devtools).
installConsoleErrorCapture();
window.addEventListener('error', (e) => {
  reportFrontendError('window-error', e.error ?? new Error(e.message));
});
window.addEventListener('unhandledrejection', (e) => {
  reportFrontendError('unhandled-rejection', e.reason);
});

installMockBridge();

const container = document.getElementById('root');

const root = createRoot(container!);

root.render(
  <React.StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </React.StrictMode>,
);
