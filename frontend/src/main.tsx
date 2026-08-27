import React from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';
import './i18n';
import App from './App';
import { ErrorBoundary } from './components/ErrorBoundary';

// Surface uncaught errors instead of failing silently: render errors
// are caught by ErrorBoundary, event-handler errors and unhandled
// rejections land on the console for diagnostics.
window.addEventListener('error', (e) => {
  console.error('opencraft uncaught error:', e.error ?? e.message);
});
window.addEventListener('unhandledrejection', (e) => {
  console.error('opencraft unhandled rejection:', e.reason);
});

const container = document.getElementById('root');

const root = createRoot(container!);

root.render(
  <React.StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </React.StrictMode>,
);
