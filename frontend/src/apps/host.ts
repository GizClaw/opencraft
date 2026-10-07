// The host half of an application's page.
//
// One application's frontend bundle is evaluated here and given the
// services it may use (the app platform plan, §2.11). The protocol is
// the plugin protocol's — a self-contained ES module, evaluated with a
// Blob URL in this window, sharing the host's React instance — but the
// services are not: an application is a leaf. It registers views for its
// own page, it talks to itself, and it reaches nothing of the
// assistant's: no workspace, no other application, no global shell.
//
// Everything in this module is keyed by application id, because that is
// the only scope an application's bundle can name. A bundle cannot ask
// about another application, and the host never answers a call for one
// with another's data.
import * as React from 'react';
import i18n from '../i18n';
import { api } from '../lib/api';
import { UIEventType } from '../lib/events';
import type { HistoryMessage, StreamPart, UIEvent } from '../lib/types';
import { defaultName, loadModule, resolveApply } from '../plugins/module';
import type * as gen from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/models';
import type * as genApps from '../../bindings/github.com/GizClaw/opencraft/internal/capabilities/apps/models';
import type {
  AppContext,
  AppServices,
  AppView,
  AppViewRegistration,
} from './types';

/** One host-side subscriber of an application's events. */
export type AppEventListener = (ev: UIEvent) => void;

type SubjectListener = (payload: unknown) => void;

// The live bundle of one application, and the pieces its disposal has to
// take back: the views it registered, the effects it asked to run on
// teardown (in reverse), and the stylesheet the host injected.
interface LiveScope {
  id: string;
  manifest: genApps.Manifest;
  views: AppView[];
  effects: (() => void)[];
  styleEl?: HTMLStyleElement;
  /** error is set when the bundle failed to apply. */
  error?: string;
}

/** A scope as the page sees it: the views, the manifest, the failure. */
export interface LiveAppScope {
  id: string;
  manifest: genApps.Manifest;
  views: AppView[];
  error?: string;
}

// One entry per application whose bundle is loaded. Only one scope is
// live in practice: the page unloads an application's bundle when its
// page stops showing it (a switch, a step back to the gallery, the page
// itself unmounting), and a registry change disposes the bundle of the
// application it changed. Views survive the tab switches *inside* the
// page, which is what a scope is for; they do not survive the page.
const scopes = new Map<string, LiveScope>();
// Event subscribers keyed by application, kept apart from the scopes:
// the built-in conversation subscribes before (or without) a bundle, and
// an application without a UI never has a scope at all.
const listeners = new Map<string, Set<AppEventListener>>();
const subjects = new Map<string, Map<string, Set<SubjectListener>>>();
// In-flight loads. StrictMode double-invokes effects in dev, and the
// page's mount and a tab switch can both ask for the same scope: a second
// evaluation would register every view twice.
const inflight = new Map<string, Promise<LiveAppScope>>();

let hostVersionRead: Promise<string> | null = null;

/** hostVersion returns the running host's version, read once. */
function readHostVersion(): Promise<string> {
  if (!hostVersionRead) {
    hostVersionRead = api
      .version()
      .then((v) => v)
      .catch(() => '');
  }
  return hostVersionRead;
}

/** decodeText turns the base64 an asset read returns into UTF-8 text. */
function decodeText(base64: string): string {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return new TextDecoder().decode(bytes);
}

/**
 * appScope returns the loaded scope of one application, if any. It is
 * how the page reads the tab list without awaiting a load it has
 * already started.
 */
export function appScope(id: string): LiveAppScope | undefined {
  const live = scopes.get(id);
  return live ? snapshot(live) : undefined;
}

/** snapshot is the page's view of one live scope. */
function snapshot(live: LiveScope): LiveAppScope {
  return {
    id: live.id,
    manifest: live.manifest,
    views: live.views,
    error: live.error,
  };
}

/**
 * ensureAppScope loads one application's bundle and applies it, once.
 *
 * The promise never rejects: a load failure (an unreadable entry, a
 * module that does not evaluate, an apply that throws) is reported in the
 * scope's `error`, and the page shows it next to the built-in
 * conversation. A bundle that loads but exports no `apply` is refused the
 * same way. An application that ships no frontend at all is *not* an
 * error: it has no views, and the page is its conversation.
 */
export function ensureAppScope(id: string): Promise<LiveAppScope> {
  const existing = scopes.get(id);
  if (existing) return Promise.resolve(appScope(id)!);
  const running = inflight.get(id);
  if (running) return running;
  const load = loadScope(id).finally(() => {
    inflight.delete(id);
  });
  inflight.set(id, load);
  return load;
}

async function loadScope(id: string): Promise<LiveAppScope> {
  const manifest = (await api.appManifest(id)) as genApps.Manifest;
  const live: LiveScope = {
    id,
    manifest,
    views: [],
    effects: [],
  };
  scopes.set(id, live);
  const entry = manifest.ui?.entry ?? '';
  if (!entry) {
    // No frontend: the application is its conversation. Not a failure —
    // the card says "conversation only", and nothing is shown as an
    // error that a user could not fix.
    return snapshot(live);
  }
  try {
    const asset = await api.appAsset(id, entry);
    const src = decodeText(asset.data);
    const ns = await loadModule('app', id, src);
    const apply = resolveApply(ns);
    if (!apply) {
      throw new Error(`app ${id}: bundle must export apply(ctx)`);
    }
    // Disposing during the load — the page left the application, an
    // update landed while its bundle was being read — has to end here: a
    // disposed scope is not resurrected, and applying into it would leave
    // views, effects and a stylesheet behind that nothing can reach. The
    // page asked for, and will ask again for, a fresh one.
    if (scopes.get(id) !== live) return snapshot(live);
    const version = await readHostVersion();
    const ctx = buildContext(id, manifest, live, version, defaultName(ns));
    await apply(ctx);
    if (scopes.get(id) !== live) return snapshot(live);
    await injectStyle(id, live, manifest.ui?.style);
  } catch (err) {
    live.error = String(err);
  }
  return snapshot(live);
}

/**
 * injectStyle loads an application's stylesheet and injects it as one
 * scoped <style> node. The node is what `data-app` is for: two
 * applications' stylesheets must not outlive their own scope, and
 * removing the node is the whole cleanup.
 */
async function injectStyle(
  id: string,
  live: LiveScope,
  styleRel?: string,
): Promise<void> {
  if (!styleRel) return;
  const asset = await api.appAsset(id, styleRel);
  if (!asset.data) return;
  const el = document.createElement('style');
  el.setAttribute('data-app', id);
  el.textContent = decodeText(asset.data);
  document.head.appendChild(el);
  live.styleEl = el;
}

/**
 * disposeAppScope unloads one application's frontend: views go away, the
 * bundle's effects run in reverse order, the stylesheet comes out of the
 * document, and the last error goes with it.
 */
export function disposeAppScope(id: string): void {
  const live = scopes.get(id);
  if (!live) return;
  scopes.delete(id);
  for (const effect of live.effects.reverse()) {
    try {
      effect();
    } catch {
      // One bad teardown must not strand the rest: the remaining
      // effects still run, and the node still leaves the document.
    }
  }
  live.styleEl?.remove();
}

/**
 * subscribeAppEvents subscribes to one application's turn traffic (its
 * stream deltas, artifacts and turn ends). The host filters by
 * application before the handler runs, so a subscriber never sees
 * another application's deltas or the assistant's.
 */
export function subscribeAppEvents(
  id: string,
  listener: AppEventListener,
): () => void {
  let set = listeners.get(id);
  if (!set) {
    set = new Set();
    listeners.set(id, set);
  }
  set.add(listener);
  return () => {
    set.delete(listener);
    if (set.size === 0) listeners.delete(id);
  };
}

/**
 * subscribeAppSubject subscribes to one subject on an application's own
 * event bus (what its scripts publish). The host delivers only events
 * the application itself produced.
 */
export function subscribeAppSubject(
  id: string,
  subject: string,
  listener: SubjectListener,
): () => void {
  let bySubject = subjects.get(id);
  if (!bySubject) {
    bySubject = new Map();
    subjects.set(id, bySubject);
  }
  let set = bySubject.get(subject);
  if (!set) {
    set = new Set();
    bySubject.set(subject, set);
  }
  set.add(listener);
  return () => {
    set.delete(listener);
    if (set.size === 0) bySubject.delete(subject);
  };
}

/**
 * dispatchAppEvent routes one backend event into one application's
 * subscribers. The application store calls it for every event that names
 * an application; the routing decision was made upstream, so this only
 * delivers.
 */
export function dispatchAppEvent(id: string, ev: UIEvent): void {
  const set = listeners.get(id);
  if (set) {
    for (const listener of [...set]) listener(ev);
  }
  if (ev.type !== UIEventType.appEvent) return;
  const data = ev.data as { subject?: string; payload?: unknown } | null;
  const subject = data?.subject ?? '';
  // Exact subject and the `*` wildcard, which is how a bundle listens to
  // its own bus without enumerating every subject it publishes.
  for (const key of [subject, '*']) {
    const bySubject = subjects.get(id)?.get(key);
    if (!bySubject) continue;
    for (const listener of [...bySubject]) listener(data?.payload);
  }
}

/** buildContext assembles the `ctx` one bundle is applied with. */
function buildContext(
  id: string,
  manifest: genApps.Manifest,
  live: LiveScope,
  version: string,
  bundleName?: string,
): AppContext {
  return {
    react: React,
    i18n: { locale: i18n.language },
    storage: buildStorage(id),
    effect: (fn: () => void) => {
      live.effects.push(fn);
    },
    app: buildServices(id, manifest, live, version, bundleName),
  };
}

/** buildStorage is ctx.storage: the application's own key/value space. */
function buildStorage(id: string) {
  return {
    get: async (key: string) => (await api.appKVGet(id, key)).value || null,
    set: (key: string, value: string) => api.appKVSet(id, key, value),
    delete: (key: string) => api.appKVDelete(id, key),
    list: async () => {
      const entries = await api.appKVList(id);
      const out: Record<string, string> = {};
      for (const e of entries) out[e.key] = e.value;
      return out;
    },
  };
}

/** buildServices is ctx.app: the application's control plane. */
function buildServices(
  id: string,
  manifest: genApps.Manifest,
  live: LiveScope,
  version: string,
  bundleName?: string,
): AppServices {
  const send = async (
    parts: StreamPart[],
    opts?: { conversationID?: string; model?: string; thinkLevel?: string },
  ): Promise<string> => {
    const start = await api.appStartTurn({
      id,
      conversation_id: opts?.conversationID ?? '',
      message: { role: 'user', content: { parts } },
      model: opts?.model ?? '',
      think: opts?.thinkLevel ?? '',
    } as unknown as gen.AppTurnRequest);
    return start.run_id;
  };
  return {
    id,
    name: manifest.name || bundleName || id,
    manifest,
    hostVersion: version,
    views: {
      add: (view: AppViewRegistration) => {
        const existing = live.views.find((v) => v.id === view.id);
        if (existing) {
          // Idempotent: a bundle that registers the same view twice (a
          // StrictMode double-apply, a retry) keeps one tab and one
          // disposer, and the second call returns the first one's.
          return () => removeView(live, view.id);
        }
        const registered: AppView = { ...view, appID: id };
        live.views.push(registered);
        live.views.sort((a, b) => (a.order ?? 0) - (b.order ?? 0));
        return () => removeView(live, view.id);
      },
    },
    sessions: async () =>
      (await api.appSessions(id)).map((s) => ({
        id: s.id,
        title: s.title,
        updatedAt: s.updated_at,
      })),
    newSession: () => api.appNewSession(id),
    deleteSession: (conversationID: string) =>
      api.appDeleteSession(id, conversationID),
    history: async (conversationID: string) =>
      (await api.appHistory(id, conversationID)) as unknown as HistoryMessage[],
    send,
    cancel: (runID: string) => api.appCancel(id, runID),
    onStream: (cb) =>
      subscribeAppEvents(id, (ev) => {
        if (ev.type !== UIEventType.stream) return;
        const data = ev.data as {
          run_id?: string;
          conversation_id?: string;
          delta?: unknown;
        };
        cb({
          runID: data.run_id ?? '',
          conversationID: data.conversation_id ?? '',
          delta: data.delta,
        });
      }),
    onTurnEnd: (cb) =>
      subscribeAppEvents(id, (ev) => {
        if (ev.type !== UIEventType.turnEnd) return;
        const data = ev.data as {
          run_id?: string;
          conversation_id?: string;
          status?: string;
          error?: string;
          error_kind?: string;
          interrupt_cause?: string;
        };
        cb({
          runID: data.run_id ?? '',
          conversationID: data.conversation_id ?? '',
          status: data.status ?? '',
          error: data.error,
          errorKind: data.error_kind,
          interruptCause: data.interrupt_cause,
        });
      }),
    on: (subject, cb) => subscribeAppSubject(id, subject, cb),
    files: {
      list: async (rel: string) =>
        (await api.appFiles(id, rel)).map((node) => ({
          name: node.name,
          path: node.path,
          dir: node.is_dir,
          size: node.size,
        })),
      read: (rel: string) => api.appReadFile(id, rel),
    },
    asset: async (rel: string) => {
      const asset = await api.appAsset(id, rel);
      const binary = atob(asset.data);
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
      return URL.createObjectURL(
        new Blob([bytes], {
          type: asset.media_type || 'application/octet-stream',
        }),
      );
    },
    reveal: (rel = '') => api.appReveal(id, rel),
    status: () => api.appStatus(id),
  };
}

function removeView(live: LiveScope, viewID: string): void {
  const index = live.views.findIndex((v) => v.id === viewID);
  if (index >= 0) live.views.splice(index, 1);
}
