import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Context } from '@cordisjs/core';
import type { PetPack } from '../pet/pack';
import type { KVService, PluginModule, SecretsService } from './types';
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
  secretExists: vi.fn(async () => true),
  secretDelete: vi.fn(async () => undefined),
  pluginKVGet: vi.fn(async () => ({ key: 'k', value: 'v' })),
  pluginKVSet: vi.fn(async () => undefined),
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
      // The other lookups, not just the property: a scope can reach a
      // service through get() or a parent link as well.
      seen.getStorage = ctx.get('storage');
      seen.getSecrets = ctx.get('secrets');
      seen.parentSecrets = (
        ctx as unknown as { parent?: { secrets?: unknown } }
      ).parent?.secrets;
      seen.react = ctx.react;
      seen.host = ctx.host;
    });

    expect(seen.storage).toBeUndefined();
    expect(seen.secrets).toBeUndefined();
    expect(seen.getStorage).toBeUndefined();
    expect(seen.getSecrets).toBeUndefined();
    expect(seen.parentSecrets).toBeUndefined();
    // The always-available services prove the plugin really ran inside
    // the app: without them the two assertions above would also pass on
    // a context that never got its services.
    expect(seen.react).toBeTruthy();
    expect((seen.host as { version: string }).version).toBe('1.2.3');
    activation.dispose();
  });

  it('keeps a granted service usable', async () => {
    const seen: Record<string, unknown> = {};
    const calls: Promise<unknown>[] = [];
    const activation = await mount(
      'with-kv',
      ['storage:kv'],
      (ctx) => {
        seen.storage = ctx.storage;
        seen.secrets = ctx.secrets;
        calls.push((ctx.storage as KVService).get('k'));
        calls.push((ctx.storage as KVService).set('k', 'v'));
      },
      ['storage'],
    );
    await Promise.all(calls);

    expect(seen.storage).toBeTruthy();
    expect(typeof (seen.storage as KVService).get).toBe('function');
    // The grant is per service: secrets stay isolated.
    expect(seen.secrets).toBeUndefined();
    // The service's own check passes for a caller that carries the
    // grant, and the calls go out under the plugin's id.
    expect(apiMock.pluginKVGet).toHaveBeenCalledWith('with-kv', 'k');
    expect(apiMock.pluginKVSet).toHaveBeenCalledWith('with-kv', 'k', 'v');
    activation.dispose();
  });

  it('keeps a granted secrets scope usable', async () => {
    const calls: Promise<unknown>[] = [];
    const activation = await mount(
      'with-secrets',
      ['secrets:auth'],
      (ctx) => {
        calls.push((ctx.secrets as SecretsService).has('OPENAI_API_KEY'));
        calls.push((ctx.secrets as SecretsService).delete('OLD_KEY'));
      },
      ['secrets'],
    );
    await Promise.all(calls);

    expect(apiMock.secretExists).toHaveBeenCalledWith('auth', 'OPENAI_API_KEY');
    expect(apiMock.secretDelete).toHaveBeenCalledWith('auth', 'OLD_KEY');
    activation.dispose();
  });

  // The root context is reachable from any scope and holds the real
  // services; isolation never hid them from it. Before the services
  // checked their caller, an unpermitted plugin could read and delete
  // auth secrets through `ctx.root.secrets`.
  it('refuses the root-context bypass for an unpermitted plugin', async () => {
    const refusals: string[] = [];
    const calls: Promise<unknown>[] = [];
    const attempt = (run: () => Promise<unknown>) => {
      try {
        calls.push(run().catch((e) => void refusals.push(String(e))));
      } catch (e) {
        // A refusal may also be a synchronous throw; either shape is a
        // refusal.
        refusals.push(String(e));
      }
    };
    const activation = await mount('no-perms', [], (ctx) => {
      const root = ctx.root as Context;
      attempt(() => (root.secrets as SecretsService).has('OPENAI_API_KEY'));
      attempt(() => (root.secrets as SecretsService).delete('OLD_KEY'));
      attempt(() => (root.storage as KVService).get('k'));
    });
    await Promise.all(calls);

    const said = refusals.join('\n');
    expect(said).toContain(
      'service "secrets" needs permission "secrets:auth", not granted by manifest',
    );
    expect(said).toContain(
      'service "storage" needs permission "storage:kv", not granted by manifest',
    );
    expect(apiMock.secretExists).not.toHaveBeenCalled();
    expect(apiMock.secretDelete).not.toHaveBeenCalled();
    expect(apiMock.pluginKVGet).not.toHaveBeenCalled();
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
