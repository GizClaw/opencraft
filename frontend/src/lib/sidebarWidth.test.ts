import { beforeEach, describe, expect, it, vi } from 'vitest';
import { installMemoryLocalStorage } from '../test/storage';
import {
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  clampSidebarWidth,
  readSidebarWidth,
  writeSidebarWidth,
} from './sidebarWidth';

describe('sidebar width', () => {
  let store: Map<string, string>;

  beforeEach(() => {
    store = installMemoryLocalStorage();
  });

  it('rounds a drag to whole pixels and keeps it inside the range', () => {
    expect(clampSidebarWidth(241.4)).toBe(241);
    expect(clampSidebarWidth(180.5)).toBe(181);
    expect(clampSidebarWidth(120)).toBe(SIDEBAR_MIN_WIDTH);
    expect(clampSidebarWidth(900)).toBe(SIDEBAR_MAX_WIDTH);
  });

  it('falls back to the default for a value that is not a number', () => {
    expect(clampSidebarWidth(Number.NaN)).toBe(SIDEBAR_DEFAULT_WIDTH);
    expect(clampSidebarWidth(Number.POSITIVE_INFINITY)).toBe(
      SIDEBAR_DEFAULT_WIDTH,
    );
  });

  it('reads back the width it wrote', () => {
    writeSidebarWidth(312.6);
    expect(store.get('oc.sidebarW')).toBe('313');
    expect(readSidebarWidth()).toBe(313);
  });

  it('uses the default when nothing is stored', () => {
    expect(readSidebarWidth()).toBe(SIDEBAR_DEFAULT_WIDTH);
  });

  it('uses the default for an entry that is not a number', () => {
    store.set('oc.sidebarW', 'wide');
    expect(readSidebarWidth()).toBe(SIDEBAR_DEFAULT_WIDTH);
    store.set('oc.sidebarW', '   ');
    expect(readSidebarWidth()).toBe(SIDEBAR_DEFAULT_WIDTH);
  });

  it('clamps a stored width that is outside the range', () => {
    store.set('oc.sidebarW', '40');
    expect(readSidebarWidth()).toBe(SIDEBAR_MIN_WIDTH);
    store.set('oc.sidebarW', '2000');
    expect(readSidebarWidth()).toBe(SIDEBAR_MAX_WIDTH);
  });

  it('survives storage that throws', () => {
    vi.spyOn(window.localStorage, 'getItem').mockImplementation(() => {
      throw new Error('denied');
    });
    vi.spyOn(window.localStorage, 'setItem').mockImplementation(() => {
      throw new Error('denied');
    });
    expect(readSidebarWidth()).toBe(SIDEBAR_DEFAULT_WIDTH);
    expect(() => writeSidebarWidth(300)).not.toThrow();
  });
});
