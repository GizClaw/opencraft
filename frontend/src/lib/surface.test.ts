import { describe, expect, it } from 'vitest';

import { surfaceOf } from './surface';

describe('surfaceOf', () => {
  it('defaults to the workbench without a surface parameter', () => {
    expect(surfaceOf('')).toBe('main');
    expect(surfaceOf('?agent=assistant')).toBe('main');
  });

  it('reads the pet window surface', () => {
    // The pet window opens exactly /?surface=pet&agent=assistant (see
    // internal/adapters/desktop/pet.go).
    expect(surfaceOf('?surface=pet&agent=assistant')).toBe('pet');
  });

  it('falls back to main for unknown surface values', () => {
    expect(surfaceOf('?surface=Pet')).toBe('main');
    expect(surfaceOf('?surface=bubble')).toBe('main');
  });
});
