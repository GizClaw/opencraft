import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppImportWizard } from './AppImportWizard';

const apiMock = vi.hoisted(() => ({
  appInspect: vi.fn(),
  appInstall: vi.fn(),
  appInstallZip: vi.fn(),
  pickFolder: vi.fn(),
  pickFile: vi.fn(),
}));

const storeMock = vi.hoisted(() => ({ load: vi.fn() }));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('../apps/store', () => ({
  useAppsStore: (select: (s: unknown) => unknown) =>
    select({ load: storeMock.load, status: {}, apps: [], busy: {} }),
}));

const summary = {
  id: 'hello',
  name: 'Hello',
  version: '1.0.0',
  enabled: true,
  agent: 'hello',
};

function inspection(over: Record<string, unknown> = {}) {
  return {
    Summary: { ...summary },
    Layers: ['layers/app.yaml'],
    Refusals: [],
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.pickFolder.mockResolvedValue('/tmp/hello');
  apiMock.pickFile.mockResolvedValue('/tmp/hello.zip');
  apiMock.appInspect.mockResolvedValue(inspection());
  apiMock.appInstall.mockResolvedValue(summary);
  apiMock.appInstallZip.mockResolvedValue(summary);
  storeMock.load.mockResolvedValue(undefined);
});

describe('AppImportWizard', () => {
  it('reads the package before anything is copied', async () => {
    render(<AppImportWizard open onClose={() => {}} onInstalled={() => {}} />);

    fireEvent.click(screen.getByTestId('app-pick-folder'));

    await waitFor(() =>
      expect(apiMock.appInspect).toHaveBeenCalledWith('/tmp/hello'),
    );
    expect(screen.getByText('Hello')).toBeTruthy();
    expect(screen.getByText(/1\.0\.0/)).toBeTruthy();
    // The preflight is a read: no install ran to find out.
    expect(apiMock.appInstall).not.toHaveBeenCalled();
  });

  it('lists every refusal and refuses to install', async () => {
    apiMock.appInspect.mockResolvedValue(
      inspection({
        Refusals: [
          {
            Layer: 'layers/app.yaml',
            Key: 'infer',
            Reason: 'a reserved key',
          },
          {
            Layer: 'layers/app.yaml',
            Key: 'agents.main',
            Reason: '{file: prompt.md} leaves the content root',
          },
        ],
      }),
    );
    render(<AppImportWizard open onClose={() => {}} onInstalled={() => {}} />);

    fireEvent.click(screen.getByTestId('app-pick-folder'));

    await waitFor(() =>
      expect(screen.getByTestId('app-refusals')).toBeTruthy(),
    );
    // One row per problem, with the layer and key that carries it: the
    // fix is per-row, which is why the verdict is data.
    expect(screen.getByText(/a reserved key/)).toBeTruthy();
    expect(screen.getByText(/leaves the content root/)).toBeTruthy();
    expect(screen.getByTestId('app-install-confirm')).toBeDisabled();
  });

  it('installs through the zip binding when the source is a zip', async () => {
    const installed = vi.fn();
    render(<AppImportWizard open onClose={() => {}} onInstalled={installed} />);

    fireEvent.click(screen.getByTestId('app-pick-zip'));
    await waitFor(() =>
      expect(apiMock.appInspect).toHaveBeenCalledWith('/tmp/hello.zip'),
    );
    fireEvent.click(screen.getByTestId('app-install-confirm'));

    await waitFor(() => expect(apiMock.appInstallZip).toHaveBeenCalledTimes(1));
    expect(apiMock.appInstallZip).toHaveBeenCalledWith('/tmp/hello.zip', {
      id: 'hello',
      name: 'Hello',
    });
    expect(apiMock.appInstall).not.toHaveBeenCalled();
    await waitFor(() => expect(installed).toHaveBeenCalledWith('hello'));
    // The list the page renders is re-read after an install, or the new
    // card is missing until a restart.
    expect(storeMock.load).toHaveBeenCalled();
  });

  it('leaves an untouched icon to the manifest and sends an edited one', async () => {
    render(<AppImportWizard open onClose={() => {}} onInstalled={() => {}} />);

    fireEvent.click(screen.getByTestId('app-pick-folder'));
    await waitFor(() =>
      expect(screen.getByTestId('app-install-confirm')).toBeTruthy(),
    );
    fireEvent.click(screen.getByTestId('app-install-confirm'));

    // Untouched: the field is absent from the options, so the install
    // keeps what the manifest wrote.
    await waitFor(() => expect(apiMock.appInstall).toHaveBeenCalledTimes(1));
    expect(apiMock.appInstall.mock.calls[0][1]).toEqual({
      id: 'hello',
      name: 'Hello',
    });

    // Cleared: the empty string is sent, which installs without an icon —
    // the field being *present* is what says "none" (the Go side takes a
    // pointer, and an absent one keeps what the manifest wrote).
    apiMock.appInstall.mockClear();
    const icon = screen.getByLabelText('Icon');
    fireEvent.change(icon, { target: { value: '🧪' } });
    fireEvent.change(icon, { target: { value: '' } });
    fireEvent.click(screen.getByTestId('app-install-confirm'));

    await waitFor(() => expect(apiMock.appInstall).toHaveBeenCalledTimes(1));
    expect(apiMock.appInstall.mock.calls[0][1]).toEqual({
      id: 'hello',
      name: 'Hello',
      icon: '',
    });
  });

  it('reports a package it cannot read a manifest out of', async () => {
    apiMock.appInspect.mockRejectedValue(
      new Error('apps: no manifest in /tmp/not-an-app'),
    );
    render(<AppImportWizard open onClose={() => {}} onInstalled={() => {}} />);

    fireEvent.click(screen.getByTestId('app-pick-folder'));

    await waitFor(() =>
      expect(
        screen.getByText('No usable application manifest in this source'),
      ).toBeTruthy(),
    );
    expect(screen.getByText(/no manifest in/)).toBeTruthy();
    expect(screen.getByTestId('app-install-confirm')).toBeDisabled();
  });
});
