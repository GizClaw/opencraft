import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { TelemetryExportCard } from './TelemetryExportCard';

const apiMock = vi.hoisted(() => ({
  telemetryExport: vi.fn(),
  setTelemetryExport: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));
vi.mock('@wailsio/runtime', () => ({
  Events: { On: vi.fn(() => vi.fn()) },
}));

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.telemetryExport.mockResolvedValue({
    enabled: true,
    configured: false,
    endpoint: '',
    insecure: false,
    headerNames: [],
    owner: '',
  });
});

describe('TelemetryExportCard', () => {
  it('shows the active sink and who installed it', async () => {
    apiMock.telemetryExport.mockResolvedValue({
      enabled: true,
      configured: true,
      endpoint: 'collector.example:4318',
      insecure: false,
      headerNames: ['Authorization'],
      owner: 'sso-plugin',
    });
    render(<TelemetryExportCard />);

    const status = await screen.findByText(/configured by sso-plugin/);
    expect(status.textContent).toContain('collector.example:4318');
    // Header names are diagnostics; header values never leave the host.
    expect(status.textContent).toContain('Authorization');
  });

  it('labels an application-owned sink and toggles the switch', async () => {
    const user = userEvent.setup();
    apiMock.telemetryExport.mockResolvedValue({
      enabled: true,
      configured: true,
      endpoint: 'app-collector.example:4318',
      insecure: true,
      headerNames: [],
      owner: '',
    });
    apiMock.setTelemetryExport.mockResolvedValue(undefined);
    render(<TelemetryExportCard />);

    const status = await screen.findByText(/configured by app configuration/);
    expect(status.textContent).toContain('plaintext');

    await user.click(screen.getByRole('button', { name: 'Block' }));
    expect(apiMock.setTelemetryExport).toHaveBeenCalledWith(false);
  });

  it('reports a refused toggle without hiding the card', async () => {
    const user = userEvent.setup();
    apiMock.setTelemetryExport.mockRejectedValue(new Error('pipeline down'));
    render(<TelemetryExportCard />);

    await screen.findByRole('button', { name: 'Block' });
    await user.click(screen.getByRole('button', { name: 'Block' }));
    expect(await screen.findByText(/pipeline down/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Allow' })).toBeInTheDocument();
  });
});
