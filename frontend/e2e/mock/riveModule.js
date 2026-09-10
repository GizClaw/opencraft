// Playwright stand-in for the `@rive-app/canvas` chunk the pet surface
// imports dynamically (frontend/src/pet/rive.ts). The e2e spec serves this
// file in place of the real chunk: the real runtime needs WASM plus a real
// .riv, and its state machine cannot be observed from the DOM.
//
// Everything above the runtime is still the production code under test —
// pack validation, the value-write de-duplication and the one-shot trigger
// sequencing in src/pet/{rive,validate,riveBindings}.ts. This module only
// has to behave like the runtime those modules talk to, and records every
// call on `window.__petRive` so the spec can assert sequences.

/** The recorded runtime log read back by frontend/e2e/pet.spec.ts. */
const log = () => {
  if (!window.__petRive) {
    window.__petRive = {
      byteLength: 0,
      artboard: '',
      stateMachine: '',
      mounted: 0,
      bound: 0,
      writes: [],
      triggers: [],
      pauses: 0,
      plays: 0,
      cleaned: 0,
    };
  }
  return window.__petRive;
};

export const EventType = { Load: 'load', LoadError: 'loadError' };

// The shipped assistant asset as its pack contract sees it: artboard `Pet`,
// state machine `PetSM`, view model `PetVM` with the eight properties the
// builtin pack binds (regenerate from assistant-default.riv if the asset
// changes; the asset contract test reads the real file).
const ARTBOARDS = [{ name: 'Pet', stateMachines: [{ name: 'PetSM' }] }];

const PROPERTIES = [
  { name: 'phase', type: 'string' },
  { name: 'tool', type: 'string' },
  { name: 'walking', type: 'boolean' },
  { name: 'sleeping', type: 'boolean' },
  { name: 'wave', type: 'trigger' },
  { name: 'look', type: 'trigger' },
  { name: 'sulk', type: 'trigger' },
  { name: 'zoomies', type: 'trigger' },
];

// Asset-side defaults. The renderer only writes a value that moved, so
// these decide what the first frame of a session reports as a write.
const DEFAULTS = {
  phase: 'Idle',
  tool: 'Busy',
  walking: false,
  sleeping: false,
};

/** Instance accessor -> property type it is allowed to read. */
const ACCESSORS = {
  string: 'string',
  enum: 'enumType',
  number: 'number',
  boolean: 'boolean',
  color: 'color',
  trigger: 'trigger',
};

class Instance {
  constructor(record) {
    this.record = record;
    this.values = { ...DEFAULTS };
  }

  /** One property handle; unknown or mistyped paths resolve to null, the
   *  way the real runtime reports a property it does not have. */
  handle(accessor, path) {
    const declared = PROPERTIES.find((property) => property.name === path);
    if (!declared || declared.type !== ACCESSORS[accessor]) return null;
    const handle = {};
    Object.defineProperty(handle, 'value', {
      get: () => this.values[path],
      set: (value) => {
        this.values[path] = value;
        this.record.writes.push({ kind: accessor, path, value });
      },
    });
    if (accessor === 'trigger') {
      Object.defineProperty(handle, 'trigger', {
        value: () => this.record.triggers.push(path),
      });
    }
    return handle;
  }

  string(path) {
    return this.handle('string', path);
  }

  enum(path) {
    return this.handle('enum', path);
  }

  number(path) {
    return this.handle('number', path);
  }

  boolean(path) {
    return this.handle('boolean', path);
  }

  color(path) {
    return this.handle('color', path);
  }

  trigger(path) {
    return this.handle('trigger', path);
  }
}

const viewModel = (record) => {
  const instance = new Instance(record);
  return {
    name: 'PetVM',
    properties: PROPERTIES,
    instance: () => instance,
    defaultInstance: () => instance,
    instanceByName: (name) => (name === 'Instance' ? instance : null),
  };
};

export class Rive {
  constructor(options) {
    const record = log();
    record.mounted += 1;
    record.byteLength = options.buffer?.byteLength ?? 0;
    record.artboard = options.artboard ?? '';
    record.stateMachine = options.stateMachine ?? '';
    this.record = record;
    this.handlers = new Map();
    // The renderer registers its Load handler synchronously once the
    // constructor returns, so the event has to wait for one microtask.
    queueMicrotask(() => this.dispatch(EventType.Load, {}));
  }

  get contents() {
    return { artboards: ARTBOARDS };
  }

  on(type, handler) {
    this.handlers.set(type, [...(this.handlers.get(type) ?? []), handler]);
  }

  off(type, handler) {
    this.handlers.set(
      type,
      (this.handlers.get(type) ?? []).filter((entry) => entry !== handler),
    );
  }

  dispatch(type, payload) {
    for (const handler of this.handlers.get(type) ?? []) handler(payload);
  }

  viewModelByName(name) {
    return name === 'PetVM' ? viewModel(this.record) : null;
  }

  viewModelByIndex(index) {
    return index === 0 ? viewModel(this.record) : null;
  }

  defaultViewModel() {
    return this.viewModelByName('PetVM');
  }

  enums() {
    return [];
  }

  bindViewModelInstance(instance) {
    if (instance) this.record.bound += 1;
  }

  resizeDrawingSurfaceToCanvas() {}

  pause() {
    this.record.pauses += 1;
  }

  play() {
    this.record.plays += 1;
  }

  cleanup() {
    this.record.cleaned += 1;
  }
}

// `@rive-app/canvas` ships as CJS, so the built bundle reaches the runtime
// through Vite's interop: import('.../rive-<hash>.js').then((m) => m.r).
// Exporting the namespace under `r` keeps the stub usable from both the
// interop path and a plain namespace import.
export const r = { EventType, Rive };
