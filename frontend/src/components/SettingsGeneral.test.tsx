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
  petSettings: vi.fn(),
  setPetSettings: vi.fn(),
  petListPacks: vi.fn(),
  petPackAsset: vi.fn(),
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
  apiMock.petSettings.mockResolvedValue({ enabled: false });
  apiMock.setPetSettings.mockResolvedValue(undefined);
  apiMock.petListPacks.mockResolvedValue([]);
  apiMock.petPackAsset.mockRejectedValue(new Error('no preview in tests'));
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

  it('persists the desktop pet toggle', async () => {
    const user = userEvent.setup();
    render(<SettingsGeneral />);

    expect(await screen.findByText(/Experimental|实验性/)).toBeInTheDocument();
    await user.click(await screen.findByRole('button', { name: /On|开启/ }));
    expect(apiMock.petSettings).toHaveBeenCalled();
    expect(apiMock.setPetSettings).toHaveBeenCalledWith({ enabled: true });
  });

  it('persists the selected pet character', async () => {
    apiMock.petListPacks.mockResolvedValue([
      {
        id: 'assistant-default',
        displayName: 'Assistant',
        version: '1.0.0',
        artboard: 'Pet',
        stateMachine: 'PetSM',
        viewModel: 'PetVM',
        rivAsset: 'builtin://assistant-default',
        meta: { scale: 1, walkSpeed: 110 },
        bindings: { phase: { type: 'string', property: 'phase' } },
      },
      {
        id: 'neko',
        displayName: 'Neko',
        version: '1.0.0',
        pluginId: 'pet-pack',
        artboard: 'Pet',
        stateMachine: 'PetSM',
        viewModel: 'PetVM',
        rivAsset: 'plugin://pet-pack/neko.riv',
        meta: { scale: 1, walkSpeed: 110 },
        bindings: { phase: { type: 'string', property: 'phase' } },
      },
    ]);
    const user = userEvent.setup();
    render(<SettingsGeneral />);

    await user.click(await screen.findByRole('button', { name: /On|开启/ }));
    await user.click(
      await screen.findByRole('button', {
        name: /Assistant pet character|宠物角色/,
      }),
    );
    await user.click(await screen.findByRole('option', { name: /Neko/ }));
    expect(apiMock.setPetSettings).toHaveBeenCalledWith({
      enabled: true,
      assistantCharacter: 'neko',
    });
  });
});
