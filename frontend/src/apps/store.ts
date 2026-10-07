import { create } from 'zustand';
import { api } from '../lib/api';
import { UIEventType } from '../lib/events';
import type * as gen from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/models';
import type * as genApps from '../../bindings/github.com/GizClaw/opencraft/internal/capabilities/apps/models';
import type { UIEvent } from '../lib/types';
import { dispatchAppEvent } from './host';

/** AppSummary is one row of the application list (Go: apps.Summary). */
export type AppSummary = genApps.Summary;

/**
 * The applications the page shows: what is installed, what each one's
 * runtime is doing, and which one the page has open.
 *
 * Kept apart from the conversation store on purpose. An application is
 * global — its content and state roots do not hang under the active
 * workspace, and its conversations never enter the sidebar's session
 * list — so nothing in here may be read as "the assistant's state".
 *
 * Events arrive from two directions and both end in this one place: the
 * user's own actions call an action (which calls the binding and then
 * reloads), and the backend's `app_changed` / `app_status` events call
 * `handleEvent`, because an install or a replacement Host can also be
 * triggered from outside this window (a second instance, a headless
 * run, a pool replacement finishing).
 */
interface AppsState {
  apps: AppSummary[];
  loading: boolean;
  /** error is the last list-level failure; a per-application failure
   *  lives on its summary (`error`) or in `lastError`. */
  error: string;
  /** openID is the application whose page is showing; empty = gallery. */
  openID: string;
  /** status caches App.Status per id (the card's dot and diagnostics). */
  status: Record<string, gen.AppStatus>;
  /** events is the last few events this page received per application,
   *  newest first: the diagnostics panel's own record, which is what a
   *  user debugging a page that did not react needs ("the backend says
   *  it happened — did anything arrive?"). */
  events: Record<string, AppRecentEvent[]>;
  /** busy marks an application with a registry write in flight. */
  busy: Record<string, boolean>;
  /**
   * revisions counts the registry changes each application has seen. The
   * bundle an open page holds is the changed install's predecessor — a
   * different version, or one whose runtime is being torn down — so the
   * count is what tells the page to unload it and load again.
   */
  revisions: Record<string, number>;

  load: () => Promise<void>;
  refreshStatus: (id: string) => Promise<void>;
  open: (id: string) => void;
  close: () => void;
  setEnabled: (id: string, enabled: boolean) => Promise<void>;
  uninstall: (id: string, purge: boolean) => Promise<void>;
  rollbackApp: (id: string) => Promise<void>;
  reloadApp: (id: string) => Promise<void>;
  revealApp: (id: string, rel?: string) => Promise<void>;
  handleEvent: (ev: UIEvent) => void;
}

// In-flight list load. React StrictMode double-invokes effects in dev,
// and install/enable flows reload the list right after their own call:
// concurrent passes must share one read, or the list flickers between an
// older and a newer answer.
let inflight: Promise<void> | null = null;

/** appID reads the scope an event names; empty = the workspace side. */
export function eventAppID(ev: UIEvent): string {
  const data = ev.data as { app_id?: string } | null | undefined;
  return data?.app_id ?? '';
}

/**
 * appEventScope reads which application an event belongs to, over every
 * event the page can receive about one: the two lifecycle events name
 * it in `id`, and everything an application causes carries `app_id`.
 * Empty means the event is not one application's — a registry-wide
 * reload, or the assistant's own business.
 */
export function appEventScope(ev: UIEvent): string {
  if (ev.type === UIEventType.appChanged || ev.type === UIEventType.appStatus) {
    const data = ev.data as { id?: string } | null | undefined;
    return data?.id ?? '';
  }
  return eventAppID(ev);
}

/**
 * AppRecentEvent is one event the page received about one application:
 * what arrived, the payload's own word for it, and when.
 */
export interface AppRecentEvent {
  type: string;
  /** detail is the payload field that changes what the row means —
   *  `assets` for an edit that touched only the bundle, the path an
   *  artifact landed on, the topic an application published. Empty when
   *  the type already says everything. */
  detail: string;
  /** at is when the page received it, in ms since epoch. */
  at: number;
}

// recentEventLimit bounds one application's ring. A page left open all
// day must not grow with every turn of a chatty application, and the
// tail of the stream is the part a person reads.
const recentEventLimit = 10;

/**
 * recordAppEvent folds one event into an application's ring: newest
 * first, bounded, and copied rather than mutated, because the store's
 * subscribers compare by reference.
 */
export function recordAppEvent(
  rings: Record<string, AppRecentEvent[]>,
  id: string,
  ev: AppRecentEvent,
): Record<string, AppRecentEvent[]> {
  return {
    ...rings,
    [id]: [ev, ...(rings[id] ?? [])].slice(0, recentEventLimit),
  };
}

/** appEventDetail names what the payload adds to the type: the fields
 *  that change the meaning of a row. A stream's deltas are not among
 *  them — see the filter in handleEvent. */
function appEventDetail(ev: UIEvent): string {
  const data = ev.data as {
    assets?: boolean;
    serving?: boolean;
    retiring?: boolean;
    subject?: string;
    status?: string;
    path?: string;
    model?: string;
  } | null;
  if (!data) return '';
  switch (ev.type) {
    case UIEventType.appChanged:
      // A bundle-only edit is the one change the page acts on without
      // re-reading the registry, so it is the one worth spelling out.
      return data.assets ? 'assets' : '';
    case UIEventType.appStatus:
      return data.retiring ? 'retiring' : data.serving ? 'serving' : 'idle';
    case UIEventType.appEvent:
      return data.subject ?? '';
    case UIEventType.turnEnd:
      return data.status ?? '';
    case UIEventType.artifact:
      return data.path ?? '';
    case UIEventType.usage:
      return data.model ?? '';
    default:
      return '';
  }
}

export const useAppsStore = create<AppsState>((set, get) => ({
  apps: [],
  loading: false,
  error: '',
  openID: '',
  status: {},
  events: {},
  busy: {},
  revisions: {},

  load: () => {
    if (inflight) return inflight;
    inflight = (async () => {
      set({ loading: true });
      try {
        const apps = await api.appList();
        set({ apps, error: '' });
      } catch (err) {
        set({ error: String(err) });
      } finally {
        set({ loading: false });
      }
    })().finally(() => {
      inflight = null;
    });
    return inflight;
  },

  refreshStatus: async (id) => {
    if (!id) return;
    try {
      const status = await api.appStatus(id);
      set((state) => ({ status: { ...state.status, [id]: status } }));
    } catch {
      // A status read is a decoration on the card; an application that
      // was uninstalled under us answers with an error, and the
      // app_changed event that follows is what removes the card.
    }
  },

  open: (id) => set({ openID: id }),
  close: () => set({ openID: '' }),

  setEnabled: async (id, enabled) => {
    set((state) => ({ busy: { ...state.busy, [id]: true } }));
    try {
      await api.appSetEnabled(id, enabled);
    } finally {
      set((state) => ({ busy: { ...state.busy, [id]: false } }));
    }
    await get().load();
    await get().refreshStatus(id);
  },

  uninstall: async (id, purge) => {
    set((state) => ({ busy: { ...state.busy, [id]: true } }));
    try {
      await api.appUninstall(id, purge);
      if (get().openID === id) set({ openID: '' });
    } finally {
      set((state) => ({ busy: { ...state.busy, [id]: false } }));
    }
    set((state) => {
      const status = { ...state.status };
      delete status[id];
      // The ring goes with it: what the page heard is about the install
      // that just left, and a reinstalled id is a different application
      // whose panel would otherwise open on someone else's history.
      const events = { ...state.events };
      delete events[id];
      return { status, events };
    });
    await get().load();
  },

  // rollbackApp puts back the version the application's last update
  // replaced. The snapshot it consumes is the registry's, so this is a
  // registry write like the install that produced it — the page only
  // decides to ask. An update that lands is what puts a card in the
  // state this action is for; a rollback whose assembly fails (the
  // restored version needs an inference wiring the user has since
  // changed) rejects, and the caller has a card to put it on.
  rollbackApp: async (id) => {
    set((state) => ({ busy: { ...state.busy, [id]: true } }));
    try {
      await api.appRollback(id);
    } finally {
      set((state) => ({ busy: { ...state.busy, [id]: false } }));
      // A rollback whose assembly failed still moved the registry — the
      // snapshot is consumed and the version it held is what is
      // installed — so the card is re-read on the way out either way.
      await get()
        .load()
        .catch(() => undefined);
      await get().refreshStatus(id);
    }
  },

  reloadApp: async (id) => {
    await api.appReload(id);
    await get().refreshStatus(id);
  },

  revealApp: async (id, rel = '') => {
    await api.appReveal(id, rel);
  },

  handleEvent: (ev) => {
    // Every event this store sees is about one application, and the
    // panel shows the page's own record of them. Stream deltas are the
    // exception they are everywhere else: a reply arrives as hundreds
    // of them, they say one thing while they do it, and the row that
    // matters — that the turn ended — follows as its own event.
    const scope = appEventScope(ev);
    if (scope && ev.type !== UIEventType.stream) {
      set((state) => ({
        events: recordAppEvent(state.events, scope, {
          type: ev.type,
          detail: appEventDetail(ev),
          at: Date.now(),
        }),
      }));
    }
    switch (ev.type) {
      case UIEventType.appChanged: {
        // The registry changed. The list is the thing to re-read — what
        // happened is decided from it, not from the event — and the
        // application's own page has to unload: an update replaced the
        // bundle it is showing, and a disable or a removal means the
        // runtime that answers its calls is going away.
        const data = ev.data as { id?: string; assets?: boolean } | null;
        const id = data?.id ?? '';
        if (id) {
          set((state) => ({
            revisions: {
              ...state.revisions,
              [id]: (state.revisions[id] ?? 0) + 1,
            },
          }));
        }
        if (data?.assets) {
          // Only its bundle changed: the backend edited the application's
          // content root for the frontend's half and left the runtime
          // standing. Nothing about the registry moved, so the list would
          // read the answer it already has; the revision above is the
          // whole job, and the page's effect on it is what loads the
          // module that was just written.
          return;
        }
        void get()
          .load()
          .then(() => {
            if (!id || get().openID !== id) return;
            const app = get().apps.find((a) => a.id === id);
            // A page showing an application that is gone or switched off
            // has nothing to show: its turns would be refused, and the
            // roots it reads are being torn down under it.
            if (!app || !app.enabled) set({ openID: '' });
          })
          .catch(() => undefined);
        if (id) void get().refreshStatus(id);
        return;
      }
      case UIEventType.appStatus: {
        const data = ev.data as { id?: string } | null;
        if (data?.id) void get().refreshStatus(data.id);
        return;
      }
      default: {
        // A turn event of an application (stream, turn_end, usage,
        // artifact, session_updated) or its own bus (app_event). The
        // page and the application's own views subscribe through
        // apps/host, which filters by the id the event names.
        const id = eventAppID(ev);
        if (id) dispatchAppEvent(id, ev);
      }
    }
  },
}));
