// Installs the browser-side Wails v3 transport used by Playwright E2E.
// In production the default HTTP transport talks to the Go process; in tests
// mock/backend.ts installs __ocMockByModule and every binding call is routed
// through setTransport to that handler table.
import { setTransport } from '@wailsio/runtime';

export function installMockBridge() {
  const win = window as unknown as {
    __ocMockByModule?: Record<string, Record<string, (...a: any[]) => unknown>>;
  };
  if (!win.__ocMockByModule) return;

  setTransport({
    call: async (_object, _method, _windowName, args) => {
      const options = (args ?? {}) as { methodName?: string; args?: unknown[] };
      const qualified = options.methodName ?? '';
      const match = /\.([A-Za-z0-9_]+)\.([A-Za-z0-9_]+)$/.exec(qualified);
      if (!match) return undefined;
      const [, struct, method] = match;
      const handler = win.__ocMockByModule?.[struct]?.[method];
      if (!handler) return undefined;
      return handler(...(options.args ?? []));
    },
  });
}
