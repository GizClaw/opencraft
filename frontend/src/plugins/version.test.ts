import { describe, expect, it } from 'vitest';
import { compareVersions } from './version';

describe('compareVersions', () => {
  it('orders core versions', () => {
    expect(compareVersions('1.0.0', '1.0.0')).toBe(0);
    expect(compareVersions('1', '1.0.0')).toBe(0);
    expect(compareVersions('1.0.1', '1.0.0')).toBe(1);
    expect(compareVersions('1.0.0', '1.0.1')).toBe(-1);
    expect(compareVersions('v2.0.0', '1.9.9')).toBe(1);
  });

  it('sorts releases above prereleases', () => {
    expect(compareVersions('1.0.0', '1.0.0-beta')).toBe(1);
    expect(compareVersions('1.0.0-beta', '1.0.0')).toBe(-1);
  });

  it('orders prerelease identifiers', () => {
    expect(compareVersions('1.0.0-beta', '1.0.0-rc.1')).toBe(-1);
    expect(compareVersions('1.0.0-alpha.2', '1.0.0-alpha.10')).toBe(-1);
    expect(compareVersions('1.0.0-1', '1.0.0-alpha')).toBe(-1);
    expect(compareVersions('1.0.0-alpha.1', '1.0.0-alpha.1.1')).toBe(-1);
  });

  it('is neutral on unparseable versions', () => {
    expect(compareVersions('abc', '1.0.0')).toBe(0);
    expect(compareVersions('', '1.0.0')).toBe(0);
  });
});
