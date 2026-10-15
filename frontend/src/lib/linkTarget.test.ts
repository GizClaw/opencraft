import { describe, expect, it, vi } from 'vitest';
import {
  codeLinkTarget,
  isExternalHref,
  resolveLinkTarget,
  splitLineSuffix,
} from './linkTarget';
import type { ResolvedTarget } from './types';

const apiMock = vi.hoisted(() => ({ resolveTarget: vi.fn() }));

vi.mock('./api', () => ({ api: apiMock }));

function target(over: Partial<ResolvedTarget> = {}): ResolvedTarget {
  return {
    path: '/tmp/w/docs/a.md',
    rel: 'docs/a.md',
    root: 'workspace',
    name: 'a.md',
    is_dir: false,
    size: 4,
    media_type: 'text/markdown',
    ...over,
  };
}

describe('resolveLinkTarget', () => {
  it('ignores empty targets and in-document anchors', async () => {
    expect(await resolveLinkTarget('   ')).toEqual({ kind: 'ignored' });
    expect(await resolveLinkTarget('#section')).toEqual({ kind: 'ignored' });
    expect(apiMock.resolveTarget).not.toHaveBeenCalled();
  });

  it('classifies URL schemes as external without resolving them', async () => {
    expect(await resolveLinkTarget('https://example.com/a')).toEqual({
      kind: 'external',
      url: 'https://example.com/a',
    });
    expect(await resolveLinkTarget('mailto:hi@example.com')).toEqual({
      kind: 'external',
      url: 'mailto:hi@example.com',
    });
    expect(apiMock.resolveTarget).not.toHaveBeenCalled();
  });

  it('treats Windows drive paths and file URLs as local targets', () => {
    expect(isExternalHref('C:\\Users\\me\\report.md')).toBe(false);
    expect(isExternalHref('report.md')).toBe(false);
    expect(isExternalHref('file:///Users/me/notes.md')).toBe(false);
    expect(isExternalHref('https://example.com')).toBe(true);
  });

  it('resolves a file URL as a local target', async () => {
    apiMock.resolveTarget.mockResolvedValueOnce(
      target({ path: '/Users/me/notes.md', rel: '', root: 'external' }),
    );
    expect(await resolveLinkTarget('file:///Users/me/notes.md')).toEqual({
      kind: 'file',
      target: target({ path: '/Users/me/notes.md', rel: '', root: 'external' }),
    });
    expect(apiMock.resolveTarget).toHaveBeenCalledWith(
      'file:///Users/me/notes.md',
      '',
    );
  });

  it('reports directories separately from files', async () => {
    apiMock.resolveTarget.mockResolvedValueOnce(target({ is_dir: true }));
    expect(await resolveLinkTarget('docs', '')).toEqual({
      kind: 'dir',
      target: target({ is_dir: true }),
    });

    apiMock.resolveTarget.mockResolvedValueOnce(target());
    expect(await resolveLinkTarget('./a.md', 'docs')).toEqual({
      kind: 'file',
      target: target(),
    });
    expect(apiMock.resolveTarget).toHaveBeenLastCalledWith('./a.md', 'docs');
  });

  it('surfaces resolve failures to the caller', async () => {
    apiMock.resolveTarget.mockRejectedValueOnce(new Error('file: missing'));
    await expect(resolveLinkTarget('missing.md')).rejects.toThrow('missing');
  });
});

// Inline code carries paths and URLs as often as it carries commands,
// and only the spans that are unmistakably one target may become links:
// a wrong guess turns a command into a button that errors when clicked.
describe('codeLinkTarget', () => {
  it('links paths written the way the assistant writes them', () => {
    expect(codeLinkTarget('internal/orchestration/host/pool.go')).toBe(
      'internal/orchestration/host/pool.go',
    );
    expect(codeLinkTarget('docs/target-architecture.md')).toBe(
      'docs/target-architecture.md',
    );
    expect(codeLinkTarget('./scripts/check-boundaries.sh')).toBe(
      './scripts/check-boundaries.sh',
    );
    expect(codeLinkTarget('~/Tools/go/bin/wails3')).toBe(
      '~/Tools/go/bin/wails3',
    );
    expect(codeLinkTarget('/etc/hosts')).toBe('/etc/hosts');
    expect(codeLinkTarget('C:\\work\\notes.md')).toBe('C:\\work\\notes.md');
    expect(codeLinkTarget('main.go')).toBe('main.go');
    expect(codeLinkTarget('Makefile')).toBe('Makefile');
  });

  it('peels a line tag so the click opens the file it points into', () => {
    expect(codeLinkTarget('internal/orchestration/host/pool.go:212')).toBe(
      'internal/orchestration/host/pool.go',
    );
    expect(codeLinkTarget('store.ts:42:7')).toBe('store.ts');
    // A tag whose head is not a path stays whole, and the whole is not a
    // path either: a time is a time.
    expect(splitLineSuffix('12:30')).toBe('12:30');
    expect(codeLinkTarget('12:30')).toBeNull();
    expect(codeLinkTarget('std::vector:12')).toBeNull();
  });

  it('links URLs, query strings included', () => {
    expect(codeLinkTarget('https://example.com/a/b?c=1')).toBe(
      'https://example.com/a/b?c=1',
    );
    expect(codeLinkTarget('mailto:hi@example.com')).toBe(
      'mailto:hi@example.com',
    );
    expect(codeLinkTarget('file:///Users/me/notes.md')).toBe(
      'file:///Users/me/notes.md',
    );
  });

  it('leaves commands, identifiers and snippets as text', () => {
    for (const span of [
      'npm test',
      'go build ./...',
      'Object.keys',
      'graph.name',
      'state.ready',
      '12:30',
      '--app-home',
      'make lint',
      'foo=bar',
      'c[i]',
      'x => y',
      'v1.26.9',
      'and/or',
      'a'.repeat(300),
    ]) {
      expect(codeLinkTarget(span)).toBeNull();
    }
  });

  it('keeps a quoted or multi-line snippet out of the link path', () => {
    expect(codeLinkTarget('"docs/a.md"')).toBeNull();
    expect(codeLinkTarget("'docs/a.md'")).toBeNull();
    expect(codeLinkTarget('docs/a.md docs/b.md')).toBeNull();
  });
});
