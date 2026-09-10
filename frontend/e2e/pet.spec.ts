// End-to-end coverage for the desktop pet surface (?surface=pet) plus the
// settings panel that reports what it mounted.
//
// The Rive runtime itself is replaced by e2e/mock/riveModule.js: the real
// one needs WASM and a real .riv, and the state machine it drives cannot be
// asserted from the DOM. Everything above it is production code — pack
// selection, pack validation, value-write de-duplication, one-shot trigger
// sequencing, the idle park and the drag gesture. The asset bytes and the
// pack definition come from the shipped files, so a pack that stops matching
// the character fails here too.
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test, type Page } from '@playwright/test';
import { handlerSources, mockBackend } from './mock/backend';

const HERE = dirname(fileURLToPath(import.meta.url));
const ASSET_DIR = join(
  resolve(HERE, '../..'),
  'internal/adapters/desktop/pet/assets',
);
const RIVE_CHUNK = /\/assets\/(rive|canvas)[^/]*\.js$/;
const RIVE_STUB = readFileSync(join(HERE, 'mock/riveModule.js'), 'utf8');

/** The pack the desktop ships, read from the file the binary embeds. */
const BUILTIN_PACK: Record<string, unknown> = JSON.parse(
  readFileSync(join(ASSET_DIR, 'assistant-default.pack.json'), 'utf8'),
);
const BUILTIN_ASSET = readFileSync(join(ASSET_DIR, 'assistant-default.riv'));
const BUILTIN_ASSET_BASE64 = BUILTIN_ASSET.toString('base64');

interface PetRiveWrite {
  kind: string;
  path: string;
  value: unknown;
}

interface PetRiveLog {
  byteLength: number;
  artboard: string;
  stateMachine: string;
  mounted: number;
  bound: number;
  writes: PetRiveWrite[];
  triggers: string[];
  pauses: number;
  plays: number;
  cleaned: number;
}

interface PetCall {
  method: string;
  args: unknown[];
}

/**
 * Boots the pet surface with the given mock backend config and the shipped
 * pack (or a variant of it). The Rive chunk is answered with the stub.
 * Pass ready=false for the cases that must never mount.
 */
async function mountPet(
  page: Page,
  config: Record<string, unknown> = {},
  pack: unknown = BUILTIN_PACK,
  ready = true,
): Promise<void> {
  await page.route(RIVE_CHUNK, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'text/javascript',
      body: RIVE_STUB,
    }),
  );
  await page.addInitScript(
    mockBackend as never,
    {
      petPacks: [pack],
      petAsset: BUILTIN_ASSET_BASE64,
      ...config,
    } as never,
  );
  await page.goto('/?surface=pet');
  if (!ready) return;
  // Fail loudly (rather than on a mysterious mount error) if the built
  // chunk name stopped matching the route pattern.
  await expect
    .poll(
      () =>
        page.evaluate(() =>
          Boolean((window as unknown as { __petRive?: unknown }).__petRive),
        ),
      {
        message: 'the Rive chunk was not replaced by e2e/mock/riveModule.js',
      },
    )
    .toBe(true);
  await expect(page.locator('.pet-surface')).toHaveAttribute(
    'data-rive-ready',
    'true',
  );
}

async function riveLog(page: Page): Promise<PetRiveLog> {
  const log = await page.evaluate(
    () => (window as unknown as { __petRive?: PetRiveLog }).__petRive,
  );
  expect(log).toBeDefined();
  return log as PetRiveLog;
}

async function petCalls(page: Page): Promise<PetCall[]> {
  return page.evaluate(
    () => (window as unknown as { __ocPetCalls: PetCall[] }).__ocPetCalls,
  );
}

async function petReports(page: Page): Promise<Record<string, unknown>[]> {
  return page.evaluate(
    () =>
      (
        window as unknown as {
          __ocPetReports: Record<string, unknown>[];
        }
      ).__ocPetReports,
  );
}

async function emitPetState(
  page: Page,
  payload: Record<string, unknown>,
): Promise<void> {
  await page.evaluate(
    (data) =>
      (
        window as unknown as {
          __emit: (name: string, value: unknown) => void;
        }
      ).__emit('pet:state', data),
    payload,
  );
}

const surface = (page: Page) => page.locator('.pet-surface');

test('mounts the shipped pack and only writes view model values that move', async ({
  page,
}) => {
  await mountPet(page);

  const log = await riveLog(page);
  expect(log.artboard).toBe('Pet');
  expect(log.stateMachine).toBe('PetSM');
  expect(log.bound).toBe(1);
  // The asset bytes travel over the binding channel unmodified.
  expect(log.byteLength).toBe(BUILTIN_ASSET.byteLength);
  // The asset sits in its idle pose already, so mounting writes nothing.
  expect(log.writes).toEqual([]);

  await emitPetState(page, { phase: 'thinking', disposition: 'work' });
  await expect(surface(page)).toHaveAttribute('data-phase', 'thinking');

  await emitPetState(page, {
    phase: 'tool',
    tool_category: 'exec',
    disposition: 'work',
  });
  await expect(surface(page)).toHaveAttribute('data-phase', 'tool');

  // An unknown tool category falls back to the pack's "busy" value.
  await emitPetState(page, {
    phase: 'tool',
    tool_category: 'browse',
    disposition: 'work',
  });

  await emitPetState(page, {
    phase: 'thinking',
    walking: true,
    disposition: 'roam',
  });
  // Repeats of the same payload must not rewrite anything: a write is what
  // re-enters state machine transitions.
  await emitPetState(page, {
    phase: 'thinking',
    walking: true,
    disposition: 'roam',
  });

  await emitPetState(page, {
    phase: 'idle',
    sleeping: true,
    disposition: 'sleep',
  });
  await expect(surface(page)).toHaveAttribute('data-phase', 'idle');

  expect((await riveLog(page)).writes).toEqual([
    { kind: 'string', path: 'phase', value: 'Thinking' },
    { kind: 'string', path: 'phase', value: 'Tool' },
    { kind: 'string', path: 'tool', value: 'Exec' },
    { kind: 'string', path: 'tool', value: 'Busy' },
    { kind: 'string', path: 'phase', value: 'Thinking' },
    { kind: 'boolean', path: 'walking', value: true },
    { kind: 'string', path: 'phase', value: 'Idle' },
    { kind: 'boolean', path: 'walking', value: false },
    { kind: 'boolean', path: 'sleeping', value: true },
  ]);
  // A healthy mount reports itself as mounted.
  expect((await petReports(page)).at(-1)).toMatchObject({
    pack_id: 'assistant-default',
    artboard: 'Pet',
    state_machine: 'PetSM',
    view_model: 'PetVM',
    ok: true,
  });
});

test('fires one-shot intents only when the intent sequence moves', async ({
  page,
}) => {
  await mountPet(page);

  await emitPetState(page, {
    phase: 'idle',
    disposition: 'roam',
    intent: 'wave',
    intent_seq: 1,
  });
  await expect(surface(page)).toHaveAttribute('data-intent', 'wave');

  // The director re-broadcasts the reaction it is playing; the renderer
  // must not replay it.
  await emitPetState(page, {
    phase: 'idle',
    disposition: 'roam',
    intent: 'wave',
    intent_seq: 1,
  });

  await emitPetState(page, {
    phase: 'idle',
    disposition: 'roam',
    intent: 'wave',
    intent_seq: 2,
  });

  await emitPetState(page, {
    phase: 'idle',
    disposition: 'roam',
    intent: 'sulk',
    intent_seq: 3,
  });
  await expect(surface(page)).toHaveAttribute('data-intent', 'sulk');

  expect((await riveLog(page)).triggers).toEqual(['wave', 'wave', 'sulk']);
});

test('parks the renderer while the window is hidden', async ({ page }) => {
  await mountPet(page);

  await emitPetState(page, { phase: 'thinking', disposition: 'work' });
  expect(await riveLog(page)).toMatchObject({ pauses: 0, plays: 1 });

  const setHidden = (hidden: boolean) =>
    page.evaluate((next) => {
      Object.defineProperty(document, 'hidden', {
        configurable: true,
        get: () => next,
      });
      document.dispatchEvent(new Event('visibilitychange'));
    }, hidden);

  await setHidden(true);
  expect(await riveLog(page)).toMatchObject({ pauses: 1, plays: 1 });

  // Payloads arriving while hidden must not restart the rAF loop.
  await emitPetState(page, { phase: 'answering', disposition: 'work' });
  expect(await riveLog(page)).toMatchObject({ pauses: 1, plays: 1 });

  await setHidden(false);
  expect(await riveLog(page)).toMatchObject({ pauses: 1, plays: 2 });
});

test('parks the renderer once the character has been still', async ({
  page,
}) => {
  await page.clock.install();
  await mountPet(page);

  await page.clock.runFor(21_000);
  expect((await riveLog(page)).pauses).toBe(1);

  // Any fresh payload wakes the character back up.
  await page.clock.runFor(0);
  await emitPetState(page, { phase: 'thinking', disposition: 'work' });
  expect((await riveLog(page)).plays).toBe(1);
});

test('degrades visibly when the pack no longer matches the asset', async ({
  page,
}) => {
  const broken = structuredClone(BUILTIN_PACK) as {
    bindings: Record<string, { type: string; property: string }>;
  };
  broken.bindings.walking.property = 'galloping';
  broken.bindings['intent:wave'].type = 'boolean';

  await mountPet(page, {}, broken);

  const marker = page.getByTestId('pet-degraded');
  await expect(marker).toBeVisible();
  await expect(surface(page)).toHaveAttribute(
    'data-runtime-status',
    'degraded',
  );
  const detail = await marker.getAttribute('title');
  expect(detail).toContain('binding "walking": property "galloping"');
  expect(detail).toContain('property "wave" is trigger, not boolean');

  // The renderer reports the mismatch instead of freezing on a still frame.
  expect((await petReports(page)).at(-1)).toMatchObject({
    pack_id: 'assistant-default',
    ok: false,
    missing: [
      'binding "intent:wave": property "wave" is trigger, not boolean',
      'binding "walking": property "galloping"',
    ],
  });

  // A property the asset does not have cannot be written.
  await emitPetState(page, {
    phase: 'thinking',
    walking: true,
    disposition: 'roam',
  });
  expect((await riveLog(page)).writes.map((write) => write.path)).toEqual([
    'phase',
  ]);
});

test('degrades visibly when the asset never arrives', async ({ page }) => {
  // A pack that is registered but whose .riv cannot be fetched (plugin
  // uninstalled, asset missing) must not leave an empty window with no
  // explanation; this is the failure the marker exists for.
  await mountPet(
    page,
    {
      handlers: handlerSources({
        'Pet.PackAsset': async () => {
          throw new Error('pet asset is gone');
        },
      }),
    },
    BUILTIN_PACK,
    false,
  );

  const marker = page.getByTestId('pet-degraded');
  await expect(marker).toBeVisible();
  await expect(surface(page)).toHaveAttribute(
    'data-runtime-status',
    'degraded',
  );
  // The character is still named, so the badge is attributable.
  await expect(surface(page)).toHaveAttribute('data-pack', 'assistant-default');
  await expect(marker).toHaveAttribute('title', /pet asset is gone/);

  expect((await petReports(page)).at(-1)).toMatchObject({
    pack_id: 'assistant-default',
    artboard: 'Pet',
    ok: false,
    error: expect.stringContaining('pet asset is gone'),
  });
});

test('degrades visibly when the pack names no asset', async ({ page }) => {
  // A pack the registry accepted but that names no .riv can never
  // render; the window must say so rather than sit empty.
  const bare = structuredClone(BUILTIN_PACK) as Record<string, unknown>;
  delete bare.rivAsset;

  await mountPet(page, {}, bare, false);

  await expect(page.getByTestId('pet-degraded')).toBeVisible();
  await expect(surface(page)).toHaveAttribute(
    'data-runtime-status',
    'degraded',
  );
  await expect(surface(page)).toHaveAttribute('data-pack', 'assistant-default');
  expect((await petReports(page)).at(-1)).toMatchObject({
    pack_id: 'assistant-default',
    ok: false,
    error: expect.stringContaining('has no riv asset'),
  });
});

test('drags the window and pokes on a click', async ({ page }) => {
  await mountPet(page);

  const box = await surface(page).boundingBox();
  expect(box).not.toBeNull();
  const startX = (box?.x ?? 0) + (box?.width ?? 0) / 2;
  const startY = (box?.y ?? 0) + (box?.height ?? 0) / 2;

  await page.mouse.move(startX, startY);
  await page.mouse.down();
  // The drag anchors on the window position, which is fetched on press.
  await page.waitForTimeout(50);
  await page.mouse.move(startX + 40, startY + 12, { steps: 4 });
  // Window moves are coalesced into a frame.
  await page.waitForTimeout(100);
  await page.mouse.up();

  const drag = (await petCalls(page)).filter(
    (call) => call.method === 'SetPosition',
  );
  expect(drag.length).toBeGreaterThan(0);
  // The mock window sits at (100, 100); the drag moved it by (40, 12).
  expect(drag.at(-1)?.args).toEqual([140, 112]);

  // A press without movement is a poke, not a drag.
  await page.mouse.move(startX, startY);
  await page.mouse.down();
  await page.mouse.up();

  expect((await petCalls(page)).slice(-2)).toEqual([
    { method: 'Poke', args: [] },
    { method: 'Activate', args: [] },
  ]);
});

test('shows the mount report in Settings > Diagnostics', async ({ page }) => {
  await page.addInitScript(
    mockBackend as never,
    {
      petRuntimeStatus: {
        pack_id: 'assistant-default',
        artboard: 'Pet',
        view_model: 'PetVM',
        ok: false,
        missing: ['binding "walking": property "galloping"'],
      },
    } as never,
  );
  await page.goto('/');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByRole('tab', { name: 'Diagnostics' }).click();

  const panel = page.getByText('Pet behavior');
  await expect(panel).toBeVisible();
  await expect(page.getByText('Degraded')).toBeVisible();
  await expect(
    page.getByText('binding "walking": property "galloping"'),
  ).toBeVisible();
});
