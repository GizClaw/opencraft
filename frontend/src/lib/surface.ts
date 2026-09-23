// Surface is which renderer document a window runs as. The desktop serves
// the workbench and its auxiliary windows from one bundle and picks between
// them with `?surface=` (see main.tsx and internal/adapters/desktop/pet.go,
// whose pet window opens exactly `/?surface=pet&agent=assistant`).
export type Surface = 'main' | 'pet';

// surfaceOf reads the surface out of a query string. An absent or unknown
// value means the workbench: a typo in a deep link must not create a third
// surface nobody renders.
export function surfaceOf(search: string = window.location.search): Surface {
  return new URLSearchParams(search).get('surface') === 'pet' ? 'pet' : 'main';
}
