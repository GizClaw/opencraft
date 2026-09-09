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
import { startRUM } from './lib/rum';
import PetSurface from './pet/PetSurface';

// Surface uncaught errors instead of failing silently: render errors
// are caught by ErrorBoundary, event-handler errors and unhandled rejections
// are forwarded to the Go telemetry pipeline (and printed for devtools).
installConsoleErrorCapture();
startRUM();
window.addEventListener('error', (e) => {
  reportFrontendError('window-error', e.error ?? new Error(e.message));
});
window.addEventListener('unhandledrejection', (e) => {
  reportFrontendError('unhandled-rejection', e.reason);
});

installMockBridge();

const container = document.getElementById('root');

const root = createRoot(container!);

const surface = new URLSearchParams(window.location.search).get('surface');

if (surface === 'pet') {
  // Pet windows must be visually transparent: style.css paints html,
  // body and #root with the app background, so mark the document and
  // clear all three before the CSS override can apply.
  document.documentElement.dataset.surface = 'pet';
  document.body.dataset.surface = 'pet';
  document.documentElement.style.background = 'transparent';
  document.body.style.background = 'transparent';
  const petRoot = document.getElementById('root');
  if (petRoot) petRoot.style.background = 'transparent';
  // Pet windows run a minimal, inert surface: no plugin host and no
  // main store. The Go side streams pet:state snapshots over Wails.
  root.render(
    <React.StrictMode>
      <PetSurface />
    </React.StrictMode>,
  );
} else {
  root.render(
    <React.StrictMode>
      <ErrorBoundary>
        <App />
      </ErrorBoundary>
    </React.StrictMode>,
  );
}
