// One ES module out of a string, evaluated in the window it will run in.
//
// The wire half of the contract is the same for both kinds of bundle the
// app hosts: a plugin's `dist/index.js` served by the plugin store, and
// an application's `ui.entry` read out of its content root. Both arrive
// as source text, both are evaluated with a Blob URL (no bare-specifier
// resolution, no custom protocol, no iframe), and both hand the host a
// module namespace whose `apply(ctx)` — or default export — is what the
// host calls.
//
// What each host does *with* the namespace differs (a plugin gets the
// Cordis context and its service branches, an application gets the scope
// in apps/host.ts), so this module stops at the namespace: resolving
// `apply` and reading `name`/`inject` stay with the callers that give
// those fields meaning.

/** The namespace one evaluated bundle produced. */
export type ModuleNamespace = Record<string, unknown>;

/**
 * Evaluates one bundle's source and returns its module namespace.
 *
 * `what` names the kind of bundle in the error text ("plugin", "app"),
 * because the loader is shared and a failure has to say which side of
 * the shell it came from.
 */
export async function loadModule(
  what: string,
  id: string,
  src: string,
): Promise<ModuleNamespace> {
  const url = URL.createObjectURL(new Blob([src], { type: 'text/javascript' }));
  try {
    return (await import(/* @vite-ignore */ url)) as ModuleNamespace;
  } catch (err) {
    throw new Error(`${what} ${id}: failed to load bundle: ${String(err)}`);
  } finally {
    URL.revokeObjectURL(url);
  }
}

/**
 * resolveApply returns the module's entry point: its `apply` export, or
 * a default export that is itself the function, or the default export's
 * own `apply`. Undefined means the bundle does not implement the
 * protocol and the caller refuses it.
 */
export function resolveApply(
  ns: ModuleNamespace,
): ((ctx: unknown) => unknown) | undefined {
  const apply = ns.apply ?? defaultApply(ns.default);
  return typeof apply === 'function'
    ? (apply as (ctx: unknown) => unknown)
    : undefined;
}

function defaultApply(mod: unknown): unknown {
  if (typeof mod === 'function') return mod;
  if (mod && typeof mod === 'object') return (mod as { apply?: unknown }).apply;
  return undefined;
}

/** defaultName reads a bundle's optional display name. */
export function defaultName(ns: ModuleNamespace): string | undefined {
  if (typeof ns.name === 'string') return ns.name;
  const mod = ns.default;
  if (mod && typeof mod === 'object') {
    const name = (mod as { name?: unknown }).name;
    if (typeof name === 'string') return name;
  }
  return undefined;
}
