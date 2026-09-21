import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { HTTPProbeCard } from './HTTPProbeCard';

const apiMock = vi.hoisted(() => ({
  httpProbe: vi.fn(),
  setHTTPProbe: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('HTTPProbeCard', () => {
  it('turns the probe on through the host and shows it recording', async () => {
    apiMock.httpProbe.mockResolvedValue({
      enabled: false,
      env: false,
      active: false,
      blocker: '',
    });
    apiMock.setHTTPProbe.mockResolvedValue({
      enabled: true,
      env: false,
      active: true,
      blocker: '',
    });

    render(<HTTPProbeCard />);

    fireEvent.click(await screen.findByRole('button', { name: 'Record' }));

    await waitFor(() =>
      expect(apiMock.setHTTPProbe).toHaveBeenCalledWith(true),
    );
    expect(await screen.findByText(/Recording:/)).toBeInTheDocument();
  });

  it('says the probe is parked while an HTTP MCP server is configured', async () => {
    apiMock.httpProbe.mockResolvedValue({
      enabled: true,
      env: false,
      active: false,
      blocker: 'HTTP MCP server "remote" is configured',
    });

    render(<HTTPProbeCard />);

    expect(
      await screen.findByText(/Parked while an HTTP MCP server/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/HTTP MCP server "remote" is configured/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Recording:/)).toBeNull();
  });

  it('keeps the switch where it was when the host rejects the change', async () => {
    apiMock.httpProbe.mockResolvedValue({
      enabled: false,
      env: false,
      active: false,
      blocker: '',
    });
    apiMock.setHTTPProbe.mockRejectedValue(new Error('save failed'));

    render(<HTTPProbeCard />);

    fireEvent.click(await screen.findByRole('button', { name: 'Record' }));

    expect(await screen.findByText(/save failed/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Off' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
  });

  it('locks the switch when the env var forces the probe on', async () => {
    apiMock.httpProbe.mockResolvedValue({
      enabled: false,
      env: true,
      active: true,
      blocker: '',
    });

    render(<HTTPProbeCard />);

    expect(
      await screen.findByRole('button', { name: 'Record' }),
    ).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Off' })).toBeDisabled();
    expect(screen.getByText(/OPENCRAFT_HTTP_PROBE forces/)).toBeInTheDocument();
  });
});
