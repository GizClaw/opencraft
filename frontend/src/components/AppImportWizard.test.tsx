import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppImportWizard } from './AppImportWizard';

const apiMock = vi.hoisted(() => ({
  appInspect: vi.fn(),
  appInstall: vi.fn(),
  appInstallZip: vi.fn(),
  appUpdate: vi.fn(),
  appUpdateZip: vi.fn(),
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
  apiMock.appUpdate.mockResolvedValue({
    ...summary,
    version: '2.0.0',
    canRollback: true,
  });
  apiMock.appUpdateZip.mockResolvedValue({
    ...summary,
    version: '2.0.0',
    canRollback: true,
  });
  storeMock.load.mockResolvedValue(undefined);
});

describe('AppImportWizard', () => {
  it('reads the package before anything is copied', async () => {
    render(
      <AppImportWizard
        open
        update={null}
        onClose={() => {}}
        onDone={() => {}}
      />,
    );

    fireEvent.click(screen.getByTestId('app-pick-folder'));

    await waitFor(() =>
      expect(apiMock.appInspect).toHaveBeenCalledWith('/tmp/hello'),
    );
    expect(screen.getByText('Hello')).toBeTruthy();
    expect(screen.getByText(/1\.0\.0/)).toBeTruthy();
    // The preflight is a read: no install ran to find out.
    expect(apiMock.appInstall).not.toHaveBeenCalled();
  });

  // The wizard is where a user agrees to what a package asks the host
  // for, so the fragments are named before the install button is live —
  // and a package that asks for none gets no section rather than an
  // empty one (the layers above are then the whole report).
  it('names the host capabilities the package asks for', async () => {
    apiMock.appInspect.mockResolvedValue(
      inspection({ Capabilities: ['tools', 'exec'] }),
    );
    render(
      <AppImportWizard
        open
        update={null}
        onClose={() => {}}
        onDone={() => {}}
      />,
    );

    fireEvent.click(screen.getByTestId('app-pick-folder'));

    const section = await screen.findByTestId('app-package-capabilities');
    expect(section).toHaveTextContent('tools');
    expect(section).toHaveTextContent('exec');
    expect(screen.getByText('Capabilities')).toBeTruthy();
  });

  it('says nothing about capabilities when the package asks for none', async () => {
    render(
      <AppImportWizard
        open
        update={null}
        onClose={() => {}}
        onDone={() => {}}
      />,
    );

    fireEvent.click(screen.getByTestId('app-pick-folder'));
    await screen.findByText('Hello');

    expect(screen.queryByTestId('app-package-capabilities')).toBeNull();
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
    render(
      <AppImportWizard
        open
        update={null}
        onClose={() => {}}
        onDone={() => {}}
      />,
    );

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
    render(
      <AppImportWizard
        open
        update={null}
        onClose={() => {}}
        onDone={installed}
      />,
    );

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
    render(
      <AppImportWizard
        open
        update={null}
        onClose={() => {}}
        onDone={() => {}}
      />,
    );

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

  it('updates the application it names, through the directory binding', async () => {
    const done = vi.fn();
    render(
      <AppImportWizard
        open
        update={{ ...summary, version: '1.0.0', canRollback: false }}
        onClose={() => {}}
        onDone={done}
      />,
    );

    fireEvent.click(screen.getByTestId('app-pick-folder'));
    await waitFor(() =>
      expect(screen.getByTestId('app-install-confirm')).toBeTruthy(),
    );
    // The identity is the installed application's, so there is nothing
    // to edit: the form is the import wizard's, not the update's.
    expect(screen.queryByLabelText('Application id')).toBeNull();
    expect(screen.getByTestId('app-install-confirm').textContent).toBe(
      'Update',
    );
    fireEvent.click(screen.getByTestId('app-install-confirm'));

    await waitFor(() => expect(apiMock.appUpdate).toHaveBeenCalledTimes(1));
    // The id is the wizard's target, not the form's, and the version it
    // installs is the package's.
    expect(apiMock.appUpdate).toHaveBeenCalledWith('hello', '/tmp/hello');
    expect(apiMock.appInstall).not.toHaveBeenCalled();
    await waitFor(() => expect(done).toHaveBeenCalledWith('hello'));
  });

  it('refuses a package meant for another application', async () => {
    apiMock.appInspect.mockResolvedValue(
      inspection({ Summary: { ...summary, id: 'other' } }),
    );
    render(
      <AppImportWizard
        open
        update={summary}
        onClose={() => {}}
        onDone={() => {}}
      />,
    );

    fireEvent.click(screen.getByTestId('app-pick-folder'));

    await waitFor(() =>
      expect(screen.getByTestId('app-update-mismatch')).toBeTruthy(),
    );
    expect(screen.getByTestId('app-update-mismatch').textContent).toMatch(
      /other/,
    );
    expect(screen.getByTestId('app-install-confirm')).toBeDisabled();
    expect(apiMock.appUpdate).not.toHaveBeenCalled();
  });

  it('keeps the error of an update that landed without serving', async () => {
    apiMock.appUpdate.mockRejectedValue(
      new Error(
        '"hello" is updated but cannot be served: graph.yaml is invalid',
      ),
    );
    render(
      <AppImportWizard
        open
        update={summary}
        onClose={() => {}}
        onDone={() => {}}
      />,
    );

    fireEvent.click(screen.getByTestId('app-pick-folder'));
    await waitFor(() =>
      expect(screen.getByTestId('app-install-confirm')).toBeTruthy(),
    );
    fireEvent.click(screen.getByTestId('app-install-confirm'));

    await waitFor(() =>
      expect(screen.getByText(/cannot be served/)).toBeTruthy(),
    );
    // The registry moved even though the call failed, so the cards behind
    // the wizard are re-read: the new version is what is installed, and
    // the rollback the update left behind is on the card.
    expect(storeMock.load).toHaveBeenCalled();
  });

  it('reports a package it cannot read a manifest out of', async () => {
    apiMock.appInspect.mockRejectedValue(
      new Error('apps: no manifest in /tmp/not-an-app'),
    );
    render(
      <AppImportWizard
        open
        update={null}
        onClose={() => {}}
        onDone={() => {}}
      />,
    );

    fireEvent.click(screen.getByTestId('app-pick-folder'));

    await waitFor(() =>
      expect(
        screen.getByText('No usable application package in this source'),
      ).toBeTruthy(),
    );
    expect(screen.getByText(/no manifest in/)).toBeTruthy();
    expect(screen.getByTestId('app-install-confirm')).toBeDisabled();
  });

  // The archive the host refuses to unpack is one of the sources this
  // block reports on, and the sentence the host wrote about it is what
  // the user has to read: which entry, how big, and the bound it broke.
  it('shows why an archive the host will not unpack is refused', async () => {
    apiMock.appInspect.mockRejectedValue(
      new Error(
        'the archive declares 64.0 MiB for "big.bin", past the 64.0 MiB ' +
          'the host accepts for one file',
      ),
    );
    render(
      <AppImportWizard
        open
        update={null}
        onClose={() => {}}
        onDone={() => {}}
      />,
    );

    fireEvent.click(screen.getByTestId('app-pick-zip'));

    await waitFor(() =>
      expect(
        screen.getByText('No usable application package in this source'),
      ).toBeTruthy(),
    );
    expect(screen.getByText(/"big\.bin"/)).toBeTruthy();
    expect(screen.getByTestId('app-install-confirm')).toBeDisabled();
  });
});
