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

export const useAppsStore = create<AppsState>((set, get) => ({
  apps: [],
  loading: false,
  error: '',
  openID: '',
  status: {},
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
      return { status };
    });
    await get().load();
  },

  reloadApp: async (id) => {
    await api.appReload(id);
    await get().refreshStatus(id);
  },

  revealApp: async (id, rel = '') => {
    await api.appReveal(id, rel);
  },

  handleEvent: (ev) => {
    switch (ev.type) {
      case UIEventType.appChanged: {
        // The registry changed. The list is the thing to re-read — what
        // happened is decided from it, not from the event — and the
        // application's own page has to unload: an update replaced the
        // bundle it is showing, and a disable or a removal means the
        // runtime that answers its calls is going away.
        const data = ev.data as { id?: string } | null;
        const id = data?.id ?? '';
        if (id) {
          set((state) => ({
            revisions: {
              ...state.revisions,
              [id]: (state.revisions[id] ?? 0) + 1,
            },
          }));
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
