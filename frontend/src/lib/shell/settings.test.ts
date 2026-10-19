// shell/settings.ts: the model switch cost notice.

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../store';
import { primeStoreTest } from '../../test/storeHarness';

vi.mock('../api', async () => ({
  api: (await import('../../test/apiMock')).apiMock,
}));
vi.mock(
  '../../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  async () => (await import('../../test/apiMock')).diagnosticsMock,
);

beforeEach(primeStoreTest);

describe('model switch cost notice', () => {
  it('warns when a conversation with content switches model', async () => {
    useStore.setState({
      conversations: {
        's-1': {
          messages: [
            {
              id: 'm-1',
              role: 'user',
              text: 'hello',
              parts: [],
              at: '2026-09-03T00:00:00Z',
            },
          ] as never,
          turnArtifacts: [],
          mode: 'workspace',
          think: 'medium',
          model: 'provider/m-1',
          pendingInteracts: [],
        },
      },
    });
    await useStore.getState().setModel('provider/m-2');
    const toasts = useStore.getState().toasts;
    expect(toasts).toHaveLength(1);
    expect(toasts[0].kind).toBe('warning');
    expect(toasts[0].text).toContain('provider/m-2');
  });

  it('stays quiet for an empty conversation, a re-pick, and no model', async () => {
    await useStore.getState().setModel('provider/m-1');
    expect(useStore.getState().toasts).toHaveLength(0);

    useStore.setState({
      conversations: {
        's-1': {
          messages: [] as never,
          turnArtifacts: [],
          mode: 'workspace',
          think: 'medium',
          model: 'provider/m-1',
          pendingInteracts: [],
        },
      },
    });
    await useStore.getState().setModel('provider/m-2');
    expect(useStore.getState().toasts).toHaveLength(0);
  });
});
