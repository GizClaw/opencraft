import { describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import {
  isExternalHref,
  linkErrorMessage,
  resolveLinkTarget,
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

  it('treats Windows drive paths as local targets', () => {
    expect(isExternalHref('C:\\Users\\me\\report.md')).toBe(false);
    expect(isExternalHref('report.md')).toBe(false);
    expect(isExternalHref('https://example.com')).toBe(true);
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

describe('linkErrorMessage', () => {
  it('maps containment rejections onto the shared copy', () => {
    const mapped = linkErrorMessage(
      new Error('file: "/etc/passwd" is outside the readable roots'),
    );
    expect(mapped).toBe(i18n.t('files.outsideRoots'));
  });

  it('keeps other failures verbatim', () => {
    expect(linkErrorMessage(new Error('file: stat "x": no such file'))).toBe(
      'Error: file: stat "x": no such file',
    );
  });
});
