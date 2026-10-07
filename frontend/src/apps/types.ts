// What an application's frontend bundle receives from the host.
//
// The contract is the plugin protocol's (§1.8 of the app platform plan)
// with an application-shaped substitution: the bundle exports apply(ctx),
// the host evaluates it in the main webview with the host's own React
// instance, and the services below are the whole surface it gets. There
// is no Cordis app on this side and no service registry: an application
// is a leaf, it registers views and talks to itself, and it can reach
// nothing of the assistant's (no workspace, no other application).
import type { ComponentType } from 'react';
import type * as genApps from '../../bindings/github.com/GizClaw/opencraft/internal/capabilities/apps/models';
import type { HistoryMessage, StreamPart } from '../lib/types';

/** One view an application's bundle registers for its page. */
export interface AppViewRegistration {
  /** id is the view's identity inside the application. */
  id: string;
  /** title is the tab label the page shows. */
  title: string;
  /** order sorts the tabs; the lowest one is selected first. */
  order?: number;
  /** Component renders the view. It receives no props: the state it
   *  needs comes from the scope it closed over. */
  Component: ComponentType;
}

/** One registered view, as the page's tab bar reads it. */
export interface AppView extends AppViewRegistration {
  /** appID is the application the view belongs to, so a shared tab bar
   *  cannot mix two applications' views. */
  appID: string;
}

/** A session of one application: the id and its display name. */
export interface AppSession {
  id: string;
  title?: string;
  updatedAt?: string;
}

/** A file out of the application's private workspace. */
export interface AppFile {
  name: string;
  path: string;
  dir: boolean;
  size?: number;
}

/** The status the card and the diagnostics panel show. */
export type AppStatus = genApps.Summary & {
  serving?: boolean;
  retiring?: boolean;
};

/**
 * AppServices is `ctx.app`: the control plane one application's bundle
 * uses to read its own state and drive its own turns. Every member is
 * scoped to the application it was created for — the host picks the id,
 * the bundle cannot name another application.
 */
export interface AppServices {
  /** id / name / manifest are the application's own metadata. */
  readonly id: string;
  readonly name: string;
  readonly manifest: genApps.Manifest;
  /** hostVersion is the running host's version (manifest
   *  minHostVersion was checked before this scope was created). */
  readonly hostVersion: string;

  /** views.add registers one view; the returned function removes it. */
  views: {
    add(view: AppViewRegistration): () => void;
  };

  /** sessions lists this application's conversations, newest first. */
  sessions(): Promise<AppSession[]>;
  /** newSession mints a conversation id without writing anything. */
  newSession(): Promise<string>;
  /** deleteSession removes one conversation. */
  deleteSession(id: string): Promise<void>;
  /** history returns the archived messages of one conversation. */
  history(id: string): Promise<HistoryMessage[]>;

  /**
   * send starts one turn. `parts` is the wire message content the
   * composer builds (text and images); the return value is the run id,
   * and an empty session id mints one. `agentID` picks which of the
   * package's agents answers — a manifest that lists several is an
   * application driving more than one of them — and an empty one runs
   * the entry agent.
   */
  send(
    parts: StreamPart[],
    opts?: {
      conversationID?: string;
      model?: string;
      thinkLevel?: string;
      agentID?: string;
    },
  ): Promise<string>;
  /** cancel stops one running turn of this application. */
  cancel(runID: string): Promise<void>;

  /**
   * onStream / onTurnEnd subscribe to this application's own turn
   * traffic. The host filters by app id before calling the handler, so a
   * subscription never sees another application's or the assistant's
   * deltas.
   */
  onStream(cb: (delta: AppStreamDelta) => void): () => void;
  onTurnEnd(cb: (end: AppTurnEnd) => void): () => void;
  /**
   * on subscribes to this application's own event bus: the subjects its
   * scripts publish (`host.publish("app.<id>.<subject>", payload)`).
   */
  on(subject: string, cb: (payload: unknown) => void): () => void;

  /** files browses the private workspace, read-only. */
  files: {
    list(rel: string): Promise<AppFile[]>;
    read(rel: string): Promise<string>;
  };
  /** asset resolves a file inside the content root to a blob: URL the
   *  DOM can load; the caller revokes it when it is done. */
  asset(rel: string): Promise<string>;
  /** reveal opens the workspace (or one file in it) in the platform
   *  file manager. */
  reveal(rel?: string): Promise<void>;
  /** status reads the assembly state and the last error. */
  status(): Promise<AppStatus>;
}

/** One streamed delta of an application turn, as its bundle sees it. */
export interface AppStreamDelta {
  runID: string;
  conversationID: string;
  delta: unknown;
}

/** One finished application turn, as its bundle sees it. */
export interface AppTurnEnd {
  runID: string;
  conversationID: string;
  status: string;
  error?: string;
  errorKind?: string;
  interruptCause?: string;
}

/** The context one application's bundle is applied with. */
export interface AppContext {
  /** react is the host's React instance (hooks are available; the
   *  bundle must not import its own). */
  react: typeof import('react');
  /** i18n carries the current locale; the application ships its own
   *  dictionaries. */
  i18n: { locale: string };
  /** storage is a key/value store namespaced to this application. */
  storage: AppStorageService;
  /** effect registers a teardown for the scope: the functions run in
   *  reverse order when the application is disabled, updated or
   *  uninstalled, and never if the scope is disposed first. */
  effect(fn: () => void): void;
  /** app is the application's control plane. */
  app: AppServices;
}

/** AppStorageService is the per-application KV surface. */
export interface AppStorageService {
  get(key: string): Promise<string | null>;
  set(key: string, value: string): Promise<void>;
  delete(key: string): Promise<void>;
  list(): Promise<Record<string, string>>;
}

/** AppScope is one loaded application frontend, as the page sees it. */
export interface AppScope {
  id: string;
  /** manifest is the parsed app.yaml the scope was loaded from: the
   *  page reads its defaults and its frontend entry. */
  manifest: genApps.Manifest;
  views: AppView[];
  /** error is set when the bundle failed to apply; the page shows it and
   *  falls back to the built-in conversation. */
  error?: string;
}
