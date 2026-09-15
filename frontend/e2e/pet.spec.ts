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
  // The pet window is a fixed 168px square stage (assistantPetWidth and
  // assistantPetHeight in internal/adapters/desktop/pet.go). The surface
  // fills whatever window it is given, so the spec has to give it the
  // real one: at the default 1280x720 viewport every layout assertion
  // about the bubble band, the centred canvas and the tool pill would be
  // measuring a window that never exists on a desktop.
  await page.setViewportSize({ width: 168, height: 168 });
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

  // While the pack is degraded the badge is the only thing on screen,
  // so it is also the only way to drag the window: it has to accept a
  // press even though it sits outside the (empty) drawing surface.
  const badge = await marker.boundingBox();
  expect(badge).not.toBeNull();
  const grabX = (badge?.x ?? 0) + (badge?.width ?? 0) / 2;
  const grabY = (badge?.y ?? 0) + (badge?.height ?? 0) / 2;
  await page.mouse.move(grabX, grabY);
  await page.mouse.down();
  await page.waitForTimeout(50);
  await page.mouse.move(grabX + 30, grabY + 20, { steps: 4 });
  await page.waitForTimeout(100);
  await page.mouse.up();

  const drag = (await petCalls(page)).filter(
    (call) => call.method === 'SetPosition',
  );
  expect(drag.at(-1)?.args).toEqual([130, 120]);
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
  // The gesture is bracketed so Go can turn it into a walk cycle.
  expect(await gestureCalls(page)).toEqual(['BeginDrag', 'EndDrag']);
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
  expect(await gestureCalls(page)).toEqual([
    'BeginDrag',
    'EndDrag',
    'BeginDrag',
    'EndDrag',
  ]);
});

/** The drag-gesture calls the surface made, in order. */
async function gestureCalls(page: Page): Promise<string[]> {
  return (await petCalls(page))
    .filter((call) => call.method === 'BeginDrag' || call.method === 'EndDrag')
    .map((call) => call.method);
}

/** Pet calls that mean the user touched the stage, in call order. */
async function petInteractions(page: Page): Promise<PetCall[]> {
  const touched = new Set(['Poke', 'Activate', 'SetPosition', 'MoveBy']);
  return (await petCalls(page)).filter((call) => touched.has(call.method));
}

/** One press-and-release at a viewport point. */
async function pressAt(page: Page, x: number, y: number): Promise<void> {
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.up();
}

interface CharacterPixels {
  /** Middle of the character's top band: solidly painted. */
  painted: { x: number; y: number };
  /** A transparent point far from any painted pixel, or null when the
   *  character covers the whole surface. */
  empty: { x: number; y: number } | null;
  /** Centre of the drawing surface in client coordinates. */
  centre: { x: number; y: number };
}

/**
 * Measures the character the real runtime actually painted, in client
 * coordinates, so the spec never hard-codes a pose or a silhouette: the
 * press targets are derived from the pixels that are on screen.
 */
async function measureCharacter(page: Page): Promise<CharacterPixels | null> {
  return page.evaluate(() => {
    const canvas = document.querySelector(
      '.pet-canvas',
    ) as HTMLCanvasElement | null;
    const context = canvas?.getContext('2d');
    if (!canvas || !context || canvas.width === 0 || canvas.height === 0) {
      return null;
    }
    const { data } = context.getImageData(0, 0, canvas.width, canvas.height);
    const alphaAt = (x: number, y: number) =>
      data[(y * canvas.width + x) * 4 + 3] ?? 0;
    const painted = (x: number, y: number) => alphaAt(x, y) > 24;
    const rect = canvas.getBoundingClientRect();
    const toClient = (x: number, y: number) => ({
      x: rect.left + (x * rect.width) / canvas.width,
      y: rect.top + (y * rect.height) / canvas.height,
    });

    const centreX = Math.round(canvas.width / 2);
    let paintedPoint: { x: number; y: number } | null = null;
    let runStart = -1;
    for (let y = 0; y <= canvas.height; y++) {
      const solid = y < canvas.height && painted(centreX, y);
      if (solid && runStart < 0) runStart = y;
      if (!solid && runStart >= 0) {
        // Middle of the first painted run down the centre line.
        paintedPoint = { x: centreX, y: Math.round((runStart + y - 1) / 2) };
        break;
      }
    }

    const clear = 6;
    let emptyPoint: { x: number; y: number } | null = null;
    for (let y = clear; y < canvas.height - clear && !emptyPoint; y += 2) {
      for (let x = clear; x < canvas.width - clear; x += 2) {
        let surrounded = true;
        for (let dy = -clear; dy <= clear && surrounded; dy++) {
          for (let dx = -clear; dx <= clear; dx++) {
            if (painted(x + dx, y + dy)) {
              surrounded = false;
              break;
            }
          }
        }
        if (surrounded) {
          emptyPoint = { x, y };
          break;
        }
      }
    }

    if (!paintedPoint) return null;
    return {
      painted: toClient(paintedPoint.x, paintedPoint.y),
      empty: emptyPoint ? toClient(emptyPoint.x, emptyPoint.y) : null,
      centre: toClient(canvas.width / 2, canvas.height / 2),
    };
  });
}

test('ignores presses that miss the drawn character', async ({ page }) => {
  await mountPet(page);

  const stage = await surface(page).boundingBox();
  const canvas = await page.locator('.pet-canvas').boundingBox();
  expect(stage).not.toBeNull();
  expect(canvas).not.toBeNull();

  // First the transparent margin of the stage, then a transparent
  // corner of the drawing surface itself: the window keeps capturing
  // those pixels, but they are not the character.
  await pressAt(page, (stage?.x ?? 0) + 4, (stage?.y ?? 0) + 4);
  await pressAt(page, (canvas?.x ?? 0) + 3, (canvas?.y ?? 0) + 3);
  expect(await petInteractions(page)).toEqual([]);

  // The character itself still answers a press.
  await pressAt(
    page,
    (canvas?.x ?? 0) + (canvas?.width ?? 0) / 2,
    (canvas?.y ?? 0) + (canvas?.height ?? 0) / 2,
  );
  expect(await petInteractions(page)).toEqual([
    { method: 'Poke', args: [] },
    { method: 'Activate', args: [] },
  ]);

  // The middle of the ring is a hole in the silhouette, and the most
  // natural place to grab the pet: it counts as the character.
  await pressAt(
    page,
    (canvas?.x ?? 0) + (canvas?.width ?? 0) / 2,
    (canvas?.y ?? 0) + (canvas?.height ?? 0) / 2,
  );
  expect(await petInteractions(page)).toEqual([
    { method: 'Poke', args: [] },
    { method: 'Activate', args: [] },
    { method: 'Poke', args: [] },
    { method: 'Activate', args: [] },
  ]);
});

test('reads the real Rive drawing surface for its hit test', async ({
  page,
}) => {
  // Every other case replaces the runtime with a drawing stub, which
  // would hide a pixel probe that silently fell back to "the whole
  // canvas": this one mounts the shipped asset through the real
  // @rive-app/canvas runtime and presses pixels it measures on screen —
  // a painted one, an empty one, and the hole in the middle — instead of
  // coordinates baked into the current character's pose.
  await page.addInitScript(
    mockBackend as never,
    {
      petPacks: [BUILTIN_PACK],
      petAsset: BUILTIN_ASSET_BASE64,
    } as never,
  );
  await page.setViewportSize({ width: 168, height: 168 });
  await page.goto('/?surface=pet');
  // The real runtime has to fetch and start wasm before it paints
  // anything, which is slower and less predictable than the stub mount;
  // wait for pixels rather than for the mount flag.
  await expect
    .poll(async () => (await measureCharacter(page)) !== null, {
      message: 'the real Rive runtime never painted the character',
      timeout: 20_000,
    })
    .toBe(true);
  const measured = await measureCharacter(page);
  expect(measured).not.toBeNull();

  if (measured?.empty) {
    await pressAt(page, measured.empty.x, measured.empty.y);
    expect(await petInteractions(page)).toEqual([]);
  }

  await pressAt(page, measured?.painted.x ?? 0, measured?.painted.y ?? 0);
  expect(await petInteractions(page)).toEqual([
    { method: 'Poke', args: [] },
    { method: 'Activate', args: [] },
  ]);

  await pressAt(page, measured?.centre.x ?? 0, measured?.centre.y ?? 0);
  expect(await petInteractions(page)).toEqual([
    { method: 'Poke', args: [] },
    { method: 'Activate', args: [] },
    { method: 'Poke', args: [] },
    { method: 'Activate', args: [] },
  ]);
});

test('treats the speech bubble and the tool pill as the pet', async ({
  page,
}) => {
  await mountPet(page);

  await emitPetState(page, {
    phase: 'idle',
    disposition: 'roam',
    intent: 'wave',
    intent_seq: 1,
  });
  const bubble = page.locator('.pet-bubble');
  await expect(bubble).toBeVisible();
  const bubbleBox = await bubble.boundingBox();
  expect(bubbleBox).not.toBeNull();
  await page.mouse.move(
    (bubbleBox?.x ?? 0) + (bubbleBox?.width ?? 0) / 2,
    (bubbleBox?.y ?? 0) + (bubbleBox?.height ?? 0) / 2,
  );
  await page.mouse.down();
  await page.mouse.up();

  await emitPetState(page, {
    phase: 'tool',
    tool_name: 'exec_command',
    tool_category: 'exec',
    disposition: 'work',
  });
  const pill = page.locator('.pet-tool');
  const pillBox = await pill.boundingBox();
  expect(pillBox).not.toBeNull();
  await page.mouse.move(
    (pillBox?.x ?? 0) + (pillBox?.width ?? 0) / 2,
    (pillBox?.y ?? 0) + (pillBox?.height ?? 0) / 2,
  );
  await page.mouse.down();
  await page.mouse.up();

  expect(await petInteractions(page)).toEqual([
    { method: 'Poke', args: [] },
    { method: 'Activate', args: [] },
    { method: 'Poke', args: [] },
    { method: 'Activate', args: [] },
  ]);
});

test('keeps the character and its overlays inside the 168px stage', async ({
  page,
}) => {
  await mountPet(page);

  const stage = await surface(page).boundingBox();
  const canvas = await page.locator('.pet-canvas').boundingBox();
  expect(stage).not.toBeNull();
  expect(canvas).not.toBeNull();
  const centreX = (box: { x: number; width: number }) => box.x + box.width / 2;
  const centreY = (box: { y: number; height: number }) =>
    box.y + box.height / 2;

  // The stage is the window the Go side creates, and the Rive artboard
  // keeps its 128px size centred on it.
  expect(Math.round(stage?.width ?? 0)).toBe(168);
  expect(Math.round(stage?.height ?? 0)).toBe(168);
  expect(Math.round(canvas?.width ?? 0)).toBe(128);
  expect(Math.round(canvas?.height ?? 0)).toBe(128);
  expect(Math.abs(centreX(canvas!) - centreX(stage!))).toBeLessThanOrEqual(1);
  expect(Math.abs(centreY(canvas!) - centreY(stage!))).toBeLessThanOrEqual(1);

  // A pet that is both asking (bubble) and running a tool (pill) draws
  // both overlays; on a stage this small either one could be clipped by
  // the window edge, which is what shrinking the stage would break.
  await emitPetState(page, {
    phase: 'tool',
    tool_name: 'exec_command',
    tool_category: 'exec',
    disposition: 'work',
    intent: 'wave',
    intent_seq: 1,
  });
  const bubble = await page.locator('.pet-bubble').boundingBox();
  const pill = await page.locator('.pet-tool').boundingBox();
  expect(bubble).not.toBeNull();
  expect(pill).not.toBeNull();
  for (const box of [bubble!, pill!]) {
    expect(box.x).toBeGreaterThanOrEqual(stage!.x);
    expect(box.y).toBeGreaterThanOrEqual(stage!.y);
    expect(box.x + box.width).toBeLessThanOrEqual(stage!.x + stage!.width);
    expect(box.y + box.height).toBeLessThanOrEqual(stage!.y + stage!.height);
  }
  // The bubble speaks from the top band, the pill sits under it.
  expect(bubble!.y + bubble!.height).toBeLessThan(pill!.y);

  // `data-interactive` is the asking pet's invitation cue: the bubble
  // wears the accent ring only while the pet wants an answer.
  const borderWhileWorking = await page
    .locator('.pet-bubble')
    .evaluate((node) => getComputedStyle(node).borderTopColor);
  await emitPetState(page, {
    phase: 'asking',
    tool_name: 'exec_command',
    tool_category: 'exec',
    disposition: 'ask',
    intent: 'wave',
    intent_seq: 1,
    interactive: true,
  });
  const borderWhileAsking = await page
    .locator('.pet-bubble')
    .evaluate((node) => getComputedStyle(node).borderTopColor);
  expect(borderWhileAsking).not.toBe(borderWhileWorking);
});

test('reports where the character is drawn', async ({ page }) => {
  await mountPet(page);

  // Go anchors the dock position and the watch spot on this box, so the
  // surface has to measure the drawn pixels: the stage itself is
  // transparent and about 40px wider than the character on each side.
  const geometryCalls = async () =>
    (await petCalls(page)).filter((call) => call.method === 'ReportGeometry');
  await expect
    .poll(async () => (await geometryCalls()).length)
    .toBeGreaterThan(0);

  const geometry = (await geometryCalls()).at(-1)?.args[0] as {
    canvas: { x: number; y: number; width: number; height: number };
    art: { x: number; y: number; width: number; height: number };
    measured: boolean;
  };
  expect(geometry.measured).toBe(true);
  expect(geometry.canvas).toEqual({ x: 20, y: 20, width: 128, height: 128 });
  // The stub paints a ring of radius 41 device px around the canvas
  // centre; allow the anti-aliased edge to land a pixel either way.
  expect(geometry.art.x).toBeGreaterThanOrEqual(42);
  expect(geometry.art.x).toBeLessThanOrEqual(44);
  expect(geometry.art.y).toBe(geometry.art.x);
  expect(geometry.art.width).toBeGreaterThanOrEqual(81);
  expect(geometry.art.width).toBeLessThanOrEqual(85);
  expect(geometry.art.height).toBe(geometry.art.width);
  // The box only moves when the pose does: the 500ms re-measure must not
  // turn into a stream of identical reports.
  expect((await geometryCalls()).length).toBeLessThanOrEqual(3);
});

test('names the running tool in a tinted pill and flags a failure', async ({
  page,
}) => {
  await mountPet(page);

  await emitPetState(page, {
    phase: 'tool',
    tool_name: 'exec_command',
    tool_category: 'exec',
    disposition: 'work',
  });

  const pill = page.locator('.pet-tool');
  await expect(pill).toBeVisible();
  await expect(pill).toHaveAttribute('data-category', 'exec');
  await expect(pill).toHaveAttribute('data-state', 'running');
  await expect(pill).toHaveAttribute('title', 'exec_command');
  await expect(pill.locator('.pet-tool__name')).toHaveText('exec_command');
  await expect(pill.locator('.pet-tool__badge > svg')).toHaveClass(
    /lucide-terminal/,
  );

  // An unknown category is not a new vocabulary entry: it degrades to
  // "other", which is what the icon map and the stylesheet both know.
  await emitPetState(page, {
    phase: 'tool',
    tool_name: 'mystery_tool',
    tool_category: 'browse',
    disposition: 'work',
  });
  await expect(pill).toHaveAttribute('data-category', 'other');
  await expect(pill.locator('.pet-tool__name')).toHaveText('mystery_tool');
  await expect(pill.locator('.pet-tool__badge > svg')).toHaveClass(
    /lucide-wrench/,
  );

  // A failed call keeps the name but flags itself.
  await emitPetState(page, {
    phase: 'error',
    tool_name: 'mystery_tool',
    tool_category: 'file',
    disposition: 'work',
  });
  await expect(pill).toHaveAttribute('data-state', 'error');
  await expect(pill.locator('.pet-tool__badge > svg')).toHaveClass(
    /lucide-circle-alert/,
  );

  // Asking is the one state that invites a click; the surface keeps
  // saying so for diagnostics even though the hit test does not branch
  // on it.
  await emitPetState(page, {
    phase: 'asking',
    tool_name: 'mystery_tool',
    tool_category: 'file',
    disposition: 'ask',
    interactive: true,
  });
  await expect(surface(page)).toHaveAttribute('data-interactive', 'true');

  // Leaving the tool phase takes the pill off the stage again.
  await emitPetState(page, { phase: 'answering', disposition: 'work' });
  await expect(page.locator('.pet-tool')).toHaveCount(0);
  await expect(surface(page)).toHaveAttribute('data-interactive', 'false');
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
