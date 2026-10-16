// The "newest request wins" guard.
//
// Half the viewer surfaces fetch on selection — a diff, a commit's file, a
// file search, a plugin path inspection — and nothing ordered those
// responses: the panel that asked second can answer first, and the older
// answer then lands on top of the newer selection (a diff body belonging to
// another file while the header names the file the user picked, a state chip
// that still says "open" for a merged PR, a spinner the stale `finally` ends
// early).
//
// The repository already solved this ad hoc in three places (`FilePreviewPane`'s
// `loadSeq`, `useFileMarks`'s `seq.current`, `PRDetailModal`'s `alive`);
// this is that guard, once. It is deliberately not a data-fetching hook:
// the guards differ in *what* they own (one panel's detail, a spinner, a
// selection), and the one thing they share is the ticket.
//
// Unmount is part of the contract, not an afterthought. A response that
// arrives after the panel closed must not write state, and a `finally`
// that ends a spinner is only allowed to do so while it still owns the
// panel — every ticket taken before unmount goes stale at once. A *newer*
// request also retires the older ticket, which is why the older `finally`
// stays quiet instead of ending the newer load's spinner.

import { useCallback, useEffect, useRef } from 'react';

export interface LatestRequest {
  /** begin claims the ticket for one request, right before it is awaited. */
  begin(): number;
  /** isCurrent reports whether `ticket` is still the newest one, on screen. */
  isCurrent(ticket: number): boolean;
}

export function useLatestRequest(): LatestRequest {
  const seq = useRef(0);
  const mounted = useRef(true);
  useEffect(
    () => () => {
      mounted.current = false;
    },
    [],
  );
  const begin = useCallback(() => ++seq.current, []);
  const isCurrent = useCallback(
    (ticket: number) => mounted.current && ticket === seq.current,
    [],
  );
  return { begin, isCurrent };
}
