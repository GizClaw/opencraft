// The two memos the render hot paths asked for, and nothing more.
//
// Long answers and big tool payloads re-render far more often than they
// change: one streaming delta re-renders the whole card, the virtual list
// unmounts and remounts rows as it scrolls, and a single render pass asks
// for the same parse from three call sites. Each of those used to re-run a
// pure function of the payload — `JSON.parse` over a still-growing args
// string, a `split('\n').length` over a four-thousand-line file, a full
// unified-diff scan — which is what turned scrolling a long conversation
// into a main-thread stall.
//
// Both helpers are keyed by the same thing the caller would parse anyway,
// so a stale answer cannot happen: the object memo keys on identity, which
// is sound for every key it has - a tool item is rebuilt when its result
// lands (`applyStream` in conv/stream.ts), so its identity changes
// whenever its content does. The one place the store appends in place is
// a streaming text block's `chunks` array (`mergeAppend`), which is why
// no memo takes a text item as its key; the value memo keys on `===`,
// which for the strings involved means "the same bytes as last time".

/**
 * createObjectMemo returns `compute` with its results cached on the identity
 * of the key object. The entry lives and dies with the object (WeakMap), so
 * a long conversation accumulates no bookkeeping.
 */
export function createObjectMemo<K extends object, V>(
  compute: (key: K) => V,
): (key: K) => V {
  const cache = new WeakMap<K, V>();
  return (key: K): V => {
    const hit = cache.get(key);
    if (hit !== undefined || cache.has(key)) return hit as V;
    const value = compute(key);
    cache.set(key, value);
    return value;
  };
}

/**
 * createLastValueMemo caches the most recent `(key, value)` pair, for keys
 * that are compared rather than held (a tool result string, a serialized
 * argument blob). Callers in one render pass ask for the same key several
 * times; a new key recomputes once.
 */
export function createLastValueMemo<K, V>(
  compute: (key: K) => V,
): (key: K) => V {
  let entry: { key: K; value: V } | null = null;
  return (key: K): V => {
    if (entry !== null && entry.key === key) return entry.value;
    const value = compute(key);
    entry = { key, value };
    return value;
  };
}
