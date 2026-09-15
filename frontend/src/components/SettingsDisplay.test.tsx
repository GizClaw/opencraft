import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DEFAULT_UI_SETTINGS } from '../lib/appearance';
import { useStore } from '../lib/store';
import { usePluginStore } from '../plugins/store';
import { installMemoryLocalStorage } from '../test/storage';
import { SettingsDisplay } from './SettingsDisplay';

const apiMock = vi.hoisted(() => ({
  setUISettings: vi.fn(),
  uiFonts: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

// The appearance card renders its controls in both locales; every query
// below accepts the English and the Chinese label.
const interfaceFontLabel = /Interface font|界面字体/;
const searchLabel = /Search fonts|搜索字体/;
const largerTextLabel = /Larger text|调大字号/;
const smallerTextLabel = /Smaller text|调小字号/;
const textSizeLabel = /^Text size$|^字号$/;

const HOST_FONTS = ['DejaVu Sans', 'Fira Code', 'PingFang SC'];

let toast: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();
  installMemoryLocalStorage();
  window.localStorage.clear();
  document.documentElement.removeAttribute('style');
  toast = vi.fn();
  usePluginStore.setState({ panels: [] });
  useStore.setState({
    theme: 'dark',
    uiSettings: DEFAULT_UI_SETTINGS,
    toast: toast as never,
  });
  apiMock.setUISettings.mockResolvedValue(undefined);
  apiMock.uiFonts.mockResolvedValue(HOST_FONTS);
});

describe('SettingsDisplay appearance', () => {
  // The trigger names the selected preset, not just the first entry in the
  // preset list (a second preset would otherwise show the wrong label).
  it('names the selected preset in the trigger', () => {
    render(<SettingsDisplay />);

    expect(
      screen.getByRole('button', { name: interfaceFontLabel }),
    ).toHaveTextContent(/System default|系统默认/);
  });

  it('applies and persists a font family from the host catalogue', async () => {
    const user = userEvent.setup();
    render(<SettingsDisplay />);

    await user.click(screen.getByRole('button', { name: interfaceFontLabel }));
    await user.click(screen.getByRole('option', { name: 'PingFang SC' }));

    expect(apiMock.setUISettings).toHaveBeenCalledWith({
      ...DEFAULT_UI_SETTINGS,
      fontFamily: 'custom',
      fontFamilyName: 'PingFang SC',
    });
    expect(
      document.documentElement.style.getPropertyValue('--oc-font-sans'),
    ).toContain("'PingFang SC'");
  });

  it('filters the catalogue while typing', async () => {
    const user = userEvent.setup();
    render(<SettingsDisplay />);

    await user.click(screen.getByRole('button', { name: interfaceFontLabel }));
    await user.type(screen.getByLabelText(searchLabel), 'fira');

    expect(screen.getByRole('option', { name: 'Fira Code' })).toBeVisible();
    expect(screen.queryByRole('option', { name: 'DejaVu Sans' })).toBeNull();
    // Presets stay reachable regardless of the query.
    expect(
      screen.getByRole('option', { name: /System default|系统默认/ }),
    ).toBeVisible();
  });

  // An input method composes text before committing it: filtering while a
  // composition is in flight re-renders the list under the IME and can drop
  // the candidate text.
  it('holds the filter while an IME composition is in flight', async () => {
    const user = userEvent.setup();
    render(<SettingsDisplay />);

    await user.click(screen.getByRole('button', { name: interfaceFontLabel }));
    const search = screen.getByLabelText(searchLabel);
    fireEvent.compositionStart(search);
    fireEvent.change(search, { target: { value: 'ziti' } });
    // Every listed family stays visible mid-composition.
    expect(screen.getByRole('option', { name: 'DejaVu Sans' })).toBeVisible();

    fireEvent.compositionEnd(search, { target: { value: 'pingfang' } });
    expect(screen.getByRole('option', { name: 'PingFang SC' })).toBeVisible();
    expect(screen.queryByRole('option', { name: 'DejaVu Sans' })).toBeNull();
  });

  it('ignores Enter while an IME composition is in flight', async () => {
    const user = userEvent.setup();
    render(<SettingsDisplay />);

    await user.click(screen.getByRole('button', { name: interfaceFontLabel }));
    screen.getByLabelText(searchLabel).dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        bubbles: true,
        isComposing: true,
      }),
    );

    // Confirming a candidate must not also pick the highlighted font.
    expect(apiMock.setUISettings).not.toHaveBeenCalled();
  });

  // The list is scrollable: a trackpad resting on it used to close the very
  // menu it was scrolling, because the anchor listener caught the list's own
  // scroll events.
  it('keeps the menu open while its own list scrolls', async () => {
    const user = userEvent.setup();
    render(<SettingsDisplay />);

    await user.click(screen.getByRole('button', { name: interfaceFontLabel }));
    fireEvent.scroll(screen.getByRole('listbox'));
    expect(screen.getByLabelText(searchLabel)).toBeVisible();

    // Scrolling something behind the menu still closes it: the anchor moved.
    fireEvent.scroll(window);
    expect(screen.queryByLabelText(searchLabel)).toBeNull();
  });

  it('steps the text size from the large A', async () => {
    const user = userEvent.setup();
    render(<SettingsDisplay />);

    await user.click(screen.getByRole('button', { name: largerTextLabel }));

    expect(apiMock.setUISettings).toHaveBeenCalledWith({
      ...DEFAULT_UI_SETTINGS,
      fontScale: 1.25,
    });
    expect(
      document.documentElement.style.getPropertyValue('--oc-root-font-size'),
    ).toBe('17.50px');
  });

  it('steps back from the small A and stops at the smallest notch', async () => {
    const user = userEvent.setup();
    render(<SettingsDisplay />);

    const smaller = screen.getByRole('button', { name: smallerTextLabel });
    await user.click(smaller);
    expect(apiMock.setUISettings).toHaveBeenLastCalledWith({
      ...DEFAULT_UI_SETTINGS,
      fontScale: 1,
    });

    // The smallest notch is the end of the range: the control disables
    // instead of wrapping or writing a value the desktop document rejects.
    expect(
      screen.getByRole('button', { name: smallerTextLabel }),
    ).toBeDisabled();
  });

  it('drags the size slider across the notches', async () => {
    render(<SettingsDisplay />);

    const slider = screen.getByRole('slider', { name: textSizeLabel });
    expect(slider).toHaveValue('1');
    expect(slider.getAttribute('aria-valuetext')).toMatch(/Standard|标准/);

    fireEvent.change(slider, { target: { value: '3' } });
    expect(
      document.documentElement.style.getPropertyValue('--oc-root-font-size'),
    ).toBe('19.60px');
    expect(apiMock.setUISettings).toHaveBeenCalledWith({
      ...DEFAULT_UI_SETTINGS,
      fontScale: 1.4,
    });
  });

  it('rolls back when the save fails', async () => {
    apiMock.setUISettings.mockRejectedValue(new Error('read-only config'));
    const user = userEvent.setup();
    render(<SettingsDisplay />);

    await user.click(screen.getByRole('button', { name: largerTextLabel }));

    await waitFor(() => {
      expect(
        document.documentElement.style.getPropertyValue('--oc-root-font-size'),
      ).toBe('15.68px');
    });
    expect(toast).toHaveBeenCalled();
  });
});
