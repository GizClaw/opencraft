import { describe, expect, it } from 'vitest';
import { PreviewCache } from './attachmentPreviews';

// The cache counts characters the way the store's media budget does, so
// the tests work in whole "previews" instead of base64.
const preview = (size: number) => 'd'.repeat(size);

describe('PreviewCache', () => {
  it('keeps the newer answer when an older request for the path lands late', async () => {
    // An evicted entry makes the next mount ask again. The first request
    // is still on its way, and its answer is the older one: landing it
    // would put a preview back that the row has already replaced, and the
    // cache would read the file twice for nothing.
    const cache = new PreviewCache(1000, 2);
    let landFirst: (url: string) => void = () => {};
    const first = cache.load(
      '/tmp/a.png',
      () =>
        new Promise((resolve) => {
          landFirst = resolve;
        }),
    );
    // Fill the two slots, then ask for /tmp/a.png again: the entry that
    // held the first request is evicted, so this is a second request.
    await cache.load('/tmp/b.png', async () => preview(10));
    await cache.load('/tmp/c.png', async () => preview(10));
    expect(cache.size).toBe(2);
    await cache.load('/tmp/a.png', async () => 'new');

    landFirst('old');
    await first;

    expect(cache.landed('/tmp/a.png')).toBe('new');
  });

  it('answers a landed preview without reading again', async () => {
    const cache = new PreviewCache();
    const read = async () => 'data:image/png;base64,AAAA';
    await cache.load('/tmp/a.png', read);

    expect(cache.landed('/tmp/a.png')).toBe('data:image/png;base64,AAAA');
    let reads = 0;
    await cache.load('/tmp/a.png', async () => {
      reads += 1;
      return 'data:image/png;base64,BBBB';
    });
    expect(reads).toBe(0);
    expect(cache.size).toBe(1);
    expect(cache.byteCount).toBe('data:image/png;base64,AAAA'.length);
  });

  it('shares one request between two rows naming one file', async () => {
    const cache = new PreviewCache();
    let reads = 0;
    let release: (url: string) => void = () => {};
    const read = () => {
      reads += 1;
      return new Promise<string>((resolve) => {
        release = resolve;
      });
    };
    const first = cache.load('/tmp/a.png', read);
    const second = cache.load('/tmp/a.png', read);
    expect(reads).toBe(1);
    release('data:image/png;base64,AAAA');
    await expect(first).resolves.toBe('data:image/png;base64,AAAA');
    await expect(second).resolves.toBe('data:image/png;base64,AAAA');
    expect(cache.landed('/tmp/a.png')).toBe('data:image/png;base64,AAAA');
  });

  it('drops the oldest preview once the budget is spent', async () => {
    // Three previews of ten characters, a budget of twenty-five: the third
    // read pushes the first out.
    const cache = new PreviewCache(25, 8);
    await cache.load('/tmp/a', async () => preview(10));
    await cache.load('/tmp/b', async () => preview(10));
    expect(cache.byteCount).toBe(20);
    await cache.load('/tmp/c', async () => preview(10));

    expect(cache.landed('/tmp/a')).toBeUndefined();
    expect(cache.landed('/tmp/b')).toBe(preview(10));
    expect(cache.landed('/tmp/c')).toBe(preview(10));
    expect(cache.byteCount).toBe(20);
  });

  it('keeps what the reader is looking at, not what arrived last', async () => {
    const cache = new PreviewCache(25, 8);
    await cache.load('/tmp/a', async () => preview(10));
    await cache.load('/tmp/b', async () => preview(10));
    // The row showing b remounts: b is now the most recent use, so the
    // preview that has to go when c lands is a.
    expect(cache.landed('/tmp/b')).toBe(preview(10));
    await cache.load('/tmp/c', async () => preview(10));

    expect(cache.landed('/tmp/a')).toBeUndefined();
    expect(cache.landed('/tmp/b')).toBe(preview(10));
    expect(cache.landed('/tmp/c')).toBe(preview(10));
  });

  it('keeps one preview even when it alone exceeds the budget', async () => {
    const cache = new PreviewCache(5, 8);
    await cache.load('/tmp/huge', async () => preview(50));
    expect(cache.landed('/tmp/huge')).toBe(preview(50));
    expect(cache.byteCount).toBe(50);
  });

  it('bounds the paths themselves, not only their payload', async () => {
    const cache = new PreviewCache(1024, 3);
    for (const name of ['a', 'b', 'c', 'd']) {
      await cache.load(`/tmp/${name}`, async () => preview(1));
    }
    expect(cache.size).toBe(3);
    expect(cache.landed('/tmp/a')).toBeUndefined();
    expect(cache.landed('/tmp/d')).toBe(preview(1));
  });

  it('forgets a failed read so the next mount tries again', async () => {
    const cache = new PreviewCache();
    await expect(
      cache.load('/tmp/gone.png', async () => {
        throw new Error('gone');
      }),
    ).rejects.toThrow('gone');
    expect(cache.landed('/tmp/gone.png')).toBeUndefined();

    let reads = 0;
    await cache.load('/tmp/gone.png', async () => {
      reads += 1;
      return preview(4);
    });
    expect(reads).toBe(1);
    expect(cache.landed('/tmp/gone.png')).toBe(preview(4));
  });

  it('does not cache an empty payload', async () => {
    const cache = new PreviewCache();
    await expect(cache.load('/tmp/empty.png', async () => '')).resolves.toBe(
      '',
    );
    expect(cache.landed('/tmp/empty.png')).toBeUndefined();
    expect(cache.size).toBe(0);
  });

  it('drops everything a workspace switch asks it to', async () => {
    const cache = new PreviewCache();
    await cache.load('/tmp/a', async () => preview(4));
    let release: (url: string) => void = () => {};
    const inFlight = cache.load(
      '/tmp/b',
      () =>
        new Promise<string>((resolve) => {
          release = resolve;
        }),
    );
    cache.clear();
    expect(cache.size).toBe(0);
    expect(cache.byteCount).toBe(0);

    // A request that was on its way when the cache was dropped must not
    // re-seed it with the workspace the window just left.
    release(preview(4));
    await expect(inFlight).resolves.toBe(preview(4));
    expect(cache.size).toBe(0);
    expect(cache.landed('/tmp/b')).toBeUndefined();
  });
});
