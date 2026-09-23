import { api } from './api';
import { activeConversationID, useStore } from './store';
import { surfaceOf } from './surface';
import type { ToolPage } from '../components/ToolsPanel';

// Renderer telemetry labels. Both reporters — the web-vitals/navigation
// reporter in rum.ts and the 30s probe in perfProbe.ts — stamp the same
// dimensions on every sample: which surface, which view was in front,
// which build, and which conversation was active when the sample was
// taken.

// Route is the view a sample was taken in, within the one surface that
// reports (the pet window runs no telemetry, see main.tsx). The surface
// alone could not attribute a long frame: the workbench is the same
// surface whether the user was scrolling a transcript, reading the
// settings page, or sitting in the agent graph editor, and those are very
// different amounts of rendering. The value is deliberately coarse — one
// label per sample, so it names the view, not the reason.
export type Route = 'welcome' | 'chat' | 'settings' | `tools:${ToolPage}`;

// routeOf reads the view in front from the same store flags App.tsx
// renders from, so nothing has to announce a route change to be counted:
// an overlay that covers the transcript is the route while it is open.
export function routeOf(): Route {
  const { configOpen, toolsView, workspace } = useStore.getState();
  if (configOpen) return 'settings';
  if (toolsView) return `tools:${toolsView}`;
  return workspace ? 'chat' : 'welcome';
}

// buildVersion is the app version, fetched once and cached. Callers await
// it so even the first sample is labeled; a failed or missing binding only
// costs the build label.
let buildPromise: Promise<string> | null = null;

export function buildVersion(): Promise<string> {
  if (buildPromise === null) {
    buildPromise = Promise.resolve()
      .then(() => api.version())
      .catch(() => '')
      .then((version) => (typeof version === 'string' ? version : ''));
  }
  return buildPromise;
}

// reportLabels are the dimensions every sample of one report carries.
// Values that are not there yet are omitted, not sent empty.
export function reportLabels(build: string): Record<string, string> {
  const labels: Record<string, string> = {
    surface: surfaceOf(),
    route: routeOf(),
  };
  if (build !== '') labels.build = build;
  const conversationID = activeConversationID();
  if (conversationID !== '') labels.conversation_id = conversationID;
  return labels;
}
