// Pins what the wails runtime stub in setup.ts exists for: importing the
// package in a test must not leave anything running against `window`. The
// real drag module arms a 50ms poll at import; when a test file ends inside
// that tick the poll outlives the environment and its callback throws
// `ReferenceError: window is not defined` as an uncaught error, which fails
// the run even though every test in it passed.
import { describe, expect, it, vi } from 'vitest';

describe('the wails runtime in tests', () => {
  it('imports without arming anything against window', async () => {
    const setInterval = vi.spyOn(window, 'setInterval');
    const setTimeout = vi.spyOn(window, 'setTimeout');
    await import('@wailsio/runtime');
    expect(setInterval).not.toHaveBeenCalled();
    expect(setTimeout).not.toHaveBeenCalled();
    setInterval.mockRestore();
    setTimeout.mockRestore();
  });

  it('answers the calls the app makes', async () => {
    const { Events, System, Window } = await import('@wailsio/runtime');
    expect(typeof Events.On('opencraft:ui', () => {})).toBe('function');
    await expect(Window.IsMaximised()).resolves.toBe(false);
    await expect(System.Environment()).resolves.toHaveProperty('OS');
  });
});
