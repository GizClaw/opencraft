// The vitest jsdom environment exposes Node's localStorage stub, whose
// setItem never round-trips (Node disables web storage without
// --localstorage-file). Tests that exercise cached preferences — theme,
// language, appearance — install this in-memory replacement instead.
export function installMemoryLocalStorage(): Map<string, string> {
  const store = new Map<string, string>();
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => {
        store.set(key, value);
      },
      removeItem: (key: string) => {
        store.delete(key);
      },
      clear: () => {
        store.clear();
      },
    },
  });
  return store;
}
