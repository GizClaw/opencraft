import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Context } from '@cordisjs/core';
import type { PetPack } from '../pet/pack';
import type { KVService, PluginModule } from './types';
import {
  activatePlugin,
  resetHost,
  setPluginModuleLoader,
  startHost,
} from './host';

// Nothing here mocks the host: resetHost() builds a real Cordis app,
// activatePlugin() mounts the plugin into it through the same code path
// production uses, and the assertions read the context the plugin's
// apply() actually received. Only the bundle loader is swapped, because
// a Node process cannot import the blob: URL the real loader builds
// (see host.ts's moduleLoader).
const apiMock = vi.hoisted(() => ({
  version: vi.fn(async () => '1.2.3'),
  petRegisterPack: vi.fn(async () => undefined),
  petUnregisterPack: vi.fn(async () => undefined),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

function pack(id: string): PetPack {
  return {
    id,
    displayName: id,
    version: '1.0.0',
    artboard: 'Pet',
    meta: { scale: 1, walkSpeed: 110 },
    bindings: { phase: { type: 'string', property: 'phase' } },
  };
}

/** Mounts one plugin whose apply() runs `body` with the real context. */
async function mount(
  id: string,
  permissions: string[],
  body: (ctx: Context) => void,
  inject: PluginModule['inject'] = [],
) {
  setPluginModuleLoader(async () => ({
    name: id,
    inject,
    apply: body,
  }));
  const activation = await activatePlugin(id, '<bundle>', permissions);
  await tick();
  return activation;
}

beforeEach(async () => {
  vi.clearAllMocks();
  await resetHost();
  await startHost();
});

afterEach(() => {
  setPluginModuleLoader(null);
});

describe('plugin service isolation', () => {
  // docs/architecture.md and host.ts both claim an unpermitted service
  // is isolated from the plugin branch, so it resolves to undefined
  // (fail-closed). Nothing tested that claim: every plugins/*.test file
  // mocked the host out. If this assertion ever fails, the host is
  // handing every plugin its KV store and the auth secret helpers.
  it('hides storage and secrets from a plugin the manifest did not grant', async () => {
    const seen: Record<string, unknown> = {};
    const activation = await mount('no-perms', [], (ctx) => {
      seen.storage = ctx.storage;
      seen.secrets = ctx.secrets;
      seen.react = ctx.react;
      seen.host = ctx.host;
    });

    expect(seen.storage).toBeUndefined();
    expect(seen.secrets).toBeUndefined();
    // The always-available services prove the plugin really ran inside
    // the app: without them the two assertions above would also pass on
    // a context that never got its services.
    expect(seen.react).toBeTruthy();
    expect((seen.host as { version: string }).version).toBe('1.2.3');
    activation.dispose();
  });

  it('keeps a granted service usable', async () => {
    const seen: Record<string, unknown> = {};
    const activation = await mount(
      'with-kv',
      ['storage:kv'],
      (ctx) => {
        seen.storage = ctx.storage;
        seen.secrets = ctx.secrets;
      },
      ['storage'],
    );

    expect(seen.storage).toBeTruthy();
    expect(typeof (seen.storage as KVService).get).toBe('function');
    // The grant is per service: secrets stay isolated.
    expect(seen.secrets).toBeUndefined();
    activation.dispose();
  });

  it('refuses a bundle that injects a service the manifest did not grant', async () => {
    setPluginModuleLoader(async () => ({
      name: 'greedy',
      inject: ['secrets'],
      apply: () => {},
    }));

    await expect(activatePlugin('greedy', '<bundle>', [])).rejects.toThrow(
      /secrets:auth/,
    );
  });
});

describe('pet pack registrar', () => {
  // F-30: the registrar unregisters what it registered, nothing else. A
  // refused registration used to be unregistered anyway, and the Go
  // store then deleted the character carrying that id — the builtin one,
  // when the plugin tried to shadow it.
  it('does not unregister a pack the registry refused', async () => {
    apiMock.petRegisterPack.mockRejectedValueOnce(
      new Error('pet: pack "assistant-default" walkSpeed must be in (0, 1200]'),
    );
    const consoleError = vi
      .spyOn(console, 'error')
      .mockImplementation(() => {});

    const activation = await mount('pet-plugin', [], (ctx) => {
      ctx.pets.add(pack('assistant-default'));
    });
    await tick();
    activation.dispose();
    await tick();

    expect(apiMock.petRegisterPack).toHaveBeenCalledTimes(1);
    expect(apiMock.petUnregisterPack).not.toHaveBeenCalled();
    consoleError.mockRestore();
  });

  it('unregisters a pack it did register, once the scope is disposed', async () => {
    const activation = await mount('pet-plugin', [], (ctx) => {
      ctx.pets.add(pack('my-cat'));
    });

    expect(apiMock.petRegisterPack).toHaveBeenCalledTimes(1);
    activation.dispose();
    await tick();

    expect(apiMock.petUnregisterPack).toHaveBeenCalledWith('my-cat');
  });
});
