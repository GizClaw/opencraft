// attachmentPreviews is the window's cache of attachment previews: one
// data URL per path, for the images a resumed transcript shows.
//
// The cache exists because a virtualized transcript mounts a row again
// every time it scrolls back into view, and a history row carries only
// the stored path: without it the same bytes crossed the bridge and were
// decoded again on every pass. It is bounded for the mirror-image reason:
// the store's own media budget (MAX_CONV_MEDIA_BYTES) decides what a
// conversation keeps inline, and a cache that never dropped anything put
// those bytes back the moment a row remounted — one screenshot-heavy
// session then held every preview it had ever scrolled past, plus the
// decoded bitmaps WebKit keeps behind each one. The budget here is the
// window's, not one conversation's: rows of several conversations mount
// through the same cache, and a workspace switch drops the lot (see
// applyWorkspaceSwitch in conv/workspace.ts).
//
// Only a landed preview is remembered. A request that fails is dropped so
// a file that comes back is picked up on the next mount instead of
// staying missing for the session, and an empty payload reads as a file
// that is gone, so it is not cached either.

// MAX_PREVIEW_CHARS bounds the data URLs the cache keeps, counted in
// string characters the way the store's media budget is (a base64 payload
// is ~1.33 characters per byte, so this is roughly 24 MiB of image).
// Sized well above one conversation's 16 MiB budget: the cache is meant
// to survive scrolling through a session, not to hold three of them.
export const MAX_PREVIEW_CHARS = 32 << 20;

// MAX_PREVIEW_ENTRIES bounds the paths themselves, so a transcript of
// thousands of small files cannot trade characters for bookkeeping. It
// also bounds the requests in flight, which are counted as no bytes.
export const MAX_PREVIEW_ENTRIES = 512;

// PreviewEntry is one path's state: either the preview that landed, or
// the request still on its way. A fetch in flight is cached as well, so
// two rows naming one file (a re-sent attachment, a remount racing the
// first response) share one request. token identifies the request that
// owns the slot: an evicted entry makes the next mount ask again, and the
// older answer must not overwrite the newer one when it lands late.
interface PreviewEntry {
  url?: string;
  request?: Promise<string>;
  token?: number;
}

// PreviewCache is a least-recently-used map from attachment path to the
// data URL that shows it. Reads and writes both count as use: a preview
// a row is showing stays, the one that fell off the top of the
// transcript goes first.
export class PreviewCache {
  private readonly entries = new Map<string, PreviewEntry>();
  private chars = 0;
  // requestSeq stamps every request, so land and drop only touch the entry
  // still owned by the request that called them.
  private requestSeq = 0;
  // generation makes clear() total: a request that was already in flight
  // when the cache was dropped must not land in the new one, or a switch
  // would re-seed the cache with the workspace it just left.
  private generation = 0;

  constructor(
    private readonly maxChars: number = MAX_PREVIEW_CHARS,
    private readonly maxEntries: number = MAX_PREVIEW_ENTRIES,
  ) {}

  // landed is the part of the cache that is already usable: a preview that
  // has arrived. An in-flight request is not a URL.
  landed(path: string): string | undefined {
    const entry = this.entries.get(path);
    if (!entry?.url) return undefined;
    this.touch(path, entry);
    return entry.url;
  }

  // load answers the preview for path, reading it through `read` when the
  // cache does not hold it yet. The promise resolves with '' when read
  // does (an empty payload: the file is gone) and rejects when the read
  // fails, so the caller keeps deciding what a missing file renders as.
  load(path: string, read: () => Promise<string>): Promise<string> {
    const cached = this.entries.get(path);
    if (cached) {
      this.touch(path, cached);
      if (cached.url) return Promise.resolve(cached.url);
      if (cached.request) return cached.request;
    }
    const generation = this.generation;
    const token = (this.requestSeq += 1);
    const request = read().then(
      (url) => {
        if (generation === this.generation) {
          if (url) this.land(path, url, token);
          else this.drop(path, token);
        }
        return url;
      },
      (err: unknown) => {
        if (generation === this.generation) this.drop(path, token);
        throw err;
      },
    );
    this.entries.set(path, { request, token });
    this.evict();
    return request;
  }

  // clear drops every preview and orphans the requests still on their way.
  // A workspace switch calls it: the paths of the workspace being left
  // name files the window is no longer looking at.
  clear() {
    this.entries.clear();
    this.chars = 0;
    this.generation += 1;
  }

  // byteCount and size are the cache's own accounting, for the tests and
  // for anyone reading the numbers off a profiler.
  get byteCount(): number {
    return this.chars;
  }

  get size(): number {
    return this.entries.size;
  }

  // land keeps one answered preview. A newer request for the same path has
  // already replaced this one when the entry was evicted and the row asked
  // again; that answer is the one a row is waiting for, so a late arrival
  // leaves it alone.
  private land(path: string, url: string, token: number) {
    const previous = this.entries.get(path);
    if (previous !== undefined && previous.token !== token) return;
    if (previous?.url) this.chars -= previous.url.length;
    this.entries.delete(path);
    this.entries.set(path, { url });
    this.chars += url.length;
    this.evict();
  }

  // drop forgets one path. A request calls it with its own token (a failed
  // read, an empty payload) and only clears the entry it still owns; evict
  // calls it with none, because a bound that has to be met drops whatever
  // is oldest, owner or not.
  private drop(path: string, token?: number) {
    const entry = this.entries.get(path);
    if (!entry) return;
    if (token !== undefined && entry.token !== token) return;
    this.entries.delete(path);
    if (entry.url) this.chars -= entry.url.length;
  }

  private touch(path: string, entry: PreviewEntry) {
    this.entries.delete(path);
    this.entries.set(path, entry);
  }

  // evict drops the oldest entries until both bounds hold. The newest
  // entry is never dropped for size: a single preview larger than the
  // whole budget (a very large image the store let through) would
  // otherwise be fetched and decoded on every mount.
  private evict() {
    while (this.entries.size > 0) {
      const overEntries = this.entries.size > this.maxEntries;
      const overChars = this.chars > this.maxChars && this.entries.size > 1;
      if (!overEntries && !overChars) return;
      const oldest = this.entries.keys().next();
      if (oldest.done) return;
      this.drop(oldest.value);
    }
  }
}

// attachmentPreviews is the window's one cache. Exported as a singleton so
// every transcript surface (and the tests) share it.
export const attachmentPreviews = new PreviewCache();
