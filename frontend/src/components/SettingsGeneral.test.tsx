import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { usePluginStore } from '../plugins/store';
import { useStore } from '../lib/store';
import { SettingsGeneral } from './SettingsGeneral';

const apiMock = vi.hoisted(() => ({
  getCloseToTray: vi.fn(),
  sessionDefaults: vi.fn(),
  saveSessionDefaults: vi.fn(),
  setCloseToTray: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

beforeEach(() => {
  vi.clearAllMocks();
  usePluginStore.setState({
    panels: [],
  });
  useStore.setState({
    sessionDefaults: { mode: 'workspace', think: 'medium' },
    toast: vi.fn(),
  });
  apiMock.getCloseToTray.mockResolvedValue(true);
  apiMock.sessionDefaults.mockResolvedValue({
    mode: 'workspace',
    think: 'medium',
  });
  apiMock.saveSessionDefaults.mockResolvedValue(undefined);
  apiMock.setCloseToTray.mockResolvedValue(undefined);
});

describe('SettingsGeneral', () => {
  it('confirms before persisting YOLO as the default mode', async () => {
    const user = userEvent.setup();
    render(<SettingsGeneral />);

    await user.click(
      screen.getByRole('button', {
        name: /Default sandbox mode|默认沙箱模式/,
      }),
    );
    await user.click(screen.getByRole('menuitem', { name: /YOLO/ }));
    const dialog = await screen.findByRole('alertdialog');
    expect(apiMock.saveSessionDefaults).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole('button', { name: /YOLO/ }));
    expect(apiMock.saveSessionDefaults).toHaveBeenCalledWith({
      mode: 'yolo',
      think: 'medium',
    });
    expect(useStore.getState().sessionDefaults).toEqual({
      mode: 'yolo',
      think: 'medium',
    });
  });

  it('saves a non-YOLO default immediately', async () => {
    const user = userEvent.setup();
    render(<SettingsGeneral />);

    await user.click(
      screen.getByRole('button', {
        name: /Default sandbox mode|默认沙箱模式/,
      }),
    );
    await user.click(screen.getByRole('menuitem', { name: /Read-only/i }));
    expect(apiMock.saveSessionDefaults).toHaveBeenCalledWith({
      mode: 'read-only',
      think: 'medium',
    });
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
  });

  it('debounces think slider changes into one save', async () => {
    vi.useFakeTimers();
    try {
      render(<SettingsGeneral />);
      const slider = screen.getByRole('slider', {
        name: /Default think level|默认思考强度/,
      });
      fireEvent.change(slider, { target: { value: '3' } });
      fireEvent.change(slider, { target: { value: '4' } });
      expect(apiMock.saveSessionDefaults).not.toHaveBeenCalled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(500);
      });

      expect(apiMock.saveSessionDefaults).toHaveBeenCalledTimes(1);
      expect(apiMock.saveSessionDefaults).toHaveBeenCalledWith({
        mode: 'workspace',
        think: 'xhigh',
      });
    } finally {
      vi.useRealTimers();
    }
  });
});
